// catdb — a cross-platform database management tool built on Wails v3.
//
// IMPORTANT: per CLAUDE.md #1, this file is the ONLY place outside
// wailsbridge/ that may import github.com/wailsapp/wails/v3/pkg/application
// directly. Everything else routes through wailsbridge.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"catdb/internal/agent"
	"catdb/internal/cli"
	"catdb/internal/core/session"
	"catdb/internal/launch"
	"catdb/internal/llm"
	"catdb/internal/llmconfig"
	"catdb/internal/platform"
	"catdb/internal/services"
	"catdb/internal/storage"
	_ "catdb/plugins"
	"catdb/wailsbridge"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[map[string]any]("transfer:progress")
	application.RegisterEvent[map[string]any]("window:close-blocked")
	application.RegisterEvent[map[string]any]("custom:switch-english-input")
	application.RegisterEvent[map[string]any]("connection:saved")
	application.RegisterEvent[map[string]any]("connection:open-request")
	application.RegisterEvent[map[string]any]("update:progress")
	application.RegisterEvent[map[string]any]("agent:delta")
	application.RegisterEvent[map[string]any]("agent:thinking")
	application.RegisterEvent[map[string]any]("agent:tool")
	application.RegisterEvent[map[string]any]("agent:usage")
	application.RegisterEvent[map[string]any]("agent:done")
	application.RegisterEvent[map[string]any]("agent:error")
	application.RegisterEvent[map[string]any]("agent:approval")
	application.RegisterEvent[map[string]any]("agent:plan")
	application.RegisterEvent[map[string]any]("agent:tx-pending")
	application.RegisterEvent[map[string]any]("agent:result")
}

func main() {
	store, err := storage.Open("")
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	// Empty service name → storage's build-tag default ("catdb" / "catdb-dev"),
	// keeping dev keyring entries separate from a production install.
	secrets := storage.NewSecrets("")
	mgr := session.NewManager(store, secrets)
	defer mgr.CloseAll()

	settingsSvc := services.NewSettingsService(store)

	agentEngine := agent.NewEngine(store, mgr, func(ctx context.Context, providerID string) (llm.Provider, error) {
		return llmconfig.Resolve(ctx, store, secrets, providerID)
	})

	agentSettingsSvc := services.NewAgentSettingsService(store, secrets)
	// Audit housekeeping: drop entries older than the retention setting
	// (default 15 days) once per launch, off the startup path.
	go agentSettingsSvc.AutoCleanAudit(context.Background())

	// Command line (DBeaver `-con` compatibility, docs/CLI_CON_DESIGN.md).
	// A request is parked, not executed: Wails discards events emitted before a
	// window exists, so the front-end pulls it after mounting
	// (SystemService.ConsumePendingLaunch).
	connSvc := services.NewConnectionService(store, secrets, mgr)
	launcher := launch.New(connSvc, store, secrets)
	// Sweep throwaway connections left behind by an abnormal exit before
	// anything reads the profile list.
	launcher.CleanupEphemeral(context.Background())
	if req, err := cli.ParseArgs(os.Args[1:], builtinVars(store)); err != nil {
		// A command line we cannot read must not stop a double-click launch.
		fmt.Fprintf(os.Stderr, "catdb: %v\n%s", err, cliUsage)
	} else {
		launcher.SetPending(req)
	}

	app := application.New(application.Options{
		Name:        "catdb",
		Description: "Cross-platform database management tool",
		Services: []application.Service{
			application.NewService(connSvc),
			application.NewService(services.NewQueryService(mgr)),
			application.NewService(services.NewMetadataService(mgr)),
			application.NewService(services.NewEditService(mgr)),
			application.NewService(services.NewTransferService(mgr)),
			application.NewService(services.NewSyncService(mgr)),
			application.NewService(services.NewSystemService(launcher)),
			application.NewService(services.NewSavedQueryService(store)),
			application.NewService(services.NewUpdateService(store, "")),
			application.NewService(settingsSvc),
			application.NewService(agentSettingsSvc),
			application.NewService(services.NewAgentService(store, agentEngine)),
			application.NewService(services.NewAgentTraceService(store)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		// One instance owns the app data (SQLite + keyring), so a second launch
		// forwards its command line here and exits. The id is namespaced by the
		// build-tag app name so a dev build never hijacks a production
		// instance's `-con` (docs/CLI_CON_DESIGN.md §4.3).
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "app." + storage.AppName(),
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				onSecondInstance(launcher, builtinVars(store), data)
			},
		},
	})

	wailsbridge.SetApp(app)

	// Listen for front-end focus events on the SQL editor — switch to English
	// input source on macOS so SQL typing starts in the correct layout.
	app.Event.On("custom:switch-english-input", func(_ *application.CustomEvent) {
		platform.SwitchToEnglishInputSource()
	})
	// Seed the native-menu locale from the persisted preference so the app
	// menu + context menus render in the right language from the first frame.
	if loc, err := settingsSvc.GetLocale(context.Background()); err == nil {
		wailsbridge.InitMenuLocale(loc)
	}
	// Same ordering rule for the theme: seed it before any window is created so
	// the native appearance (titlebar, backdrop, webview colour scheme) is right
	// from the first frame instead of flashing the system theme.
	if theme, err := settingsSvc.GetTheme(context.Background()); err == nil {
		wailsbridge.InitTheme(theme)
	}
	app.Menu.SetApplicationMenu(wailsbridge.BuildApplicationMenu(app))
	wailsbridge.RegisterContextMenus(app)

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "catdb",
		Width:     1200,
		Height:    760,
		MinWidth:  600,
		MinHeight: 500,
		Frameless: runtime.GOOS == "windows",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 30,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
			Appearance:              wailsbridge.MacAppearance(),
		},
		Windows: application.WindowsWindow{
			Theme: wailsbridge.WindowsTheme(),
		},
		BackgroundColour: wailsbridge.WindowBackground(),
		URL:              "/",
	})
	wailsbridge.AttachCloseGuard(win)
	wailsbridge.SetMainWindow(win)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// cliUsage is printed to stderr when the command line cannot be parsed. It is
// technical output, deliberately English and un-localized (CLAUDE.md i18n:
// user-visible UI copy goes through i18n, launch diagnostics do not).
const cliUsage = `usage: catdb [-con <spec>]... [-vars <file>]
  -con <spec>   connection specification, DBeaver syntax:
                "driver=mysql|host=localhost|port=3306|database=app|user=root|password=secret"
                or an existing connection's name/id: "-con MyConnection"
  -vars <file>  Java properties file feeding ${variable} expansion
`

// builtinVars supplies the ${…} names catdb can answer on its own.
// ${application.version} is left to the -vars file or the environment: the Go
// binary carries no version constant (the updater compares the front-end's
// VITE_APP_VERSION), so inventing one here would only lie.
func builtinVars(store *storage.Store) cli.VarResolver {
	return cli.NewBuiltinResolver(cli.Environment{
		Workspace: filepath.Dir(store.Path()),
	})
}

// onSecondInstance handles a command line forwarded by a second process: the
// app is already up and its window is ready, so the request runs immediately
// and the result is pushed to the front-end — the opposite of the cold-start
// path, which parks the request for the front-end to pull.
func onSecondInstance(launcher *launch.Launcher, vars cli.VarResolver, data application.SecondInstanceData) {
	req, err := cli.ParseArgs(stripProgramName(data.Args), vars)
	if err != nil {
		log.Printf("catdb: second instance: %v", err)
		wailsbridge.FocusMainWindow()
		return
	}
	if req.IsEmpty() {
		// Plain re-launch (double-click while running) — just come forward.
		wailsbridge.FocusMainWindow()
		return
	}
	outcome := launcher.ExecuteLaunch(context.Background(), req)
	wailsbridge.Emit("connection:open-request", map[string]any{
		"connectionIds": outcome.ConnectionIDs,
		"consoleIds":    outcome.ConsoleIDs,
		"errors":        outcome.Errors,
	})
	wailsbridge.FocusMainWindow()
}

// stripProgramName drops argv[0]. Wails forwards the second instance's full
// os.Args, and cli.ParseArgs expects os.Args[1:]; the check keeps a
// already-trimmed slice (or a future Wails change) working.
func stripProgramName(args []string) []string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[1:]
	}
	return args
}
