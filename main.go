// catdb — a cross-platform database management tool built on Wails v3.
//
// IMPORTANT: per CLAUDE.md #1, this file is the ONLY place outside
// wailsbridge/ that may import github.com/wailsapp/wails/v3/pkg/application
// directly. Everything else routes through wailsbridge.
package main

import (
	"context"
	"embed"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"

	"catdb/internal/agent"
	"catdb/internal/core/session"
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

	app := application.New(application.Options{
		Name:        "catdb",
		Description: "Cross-platform database management tool",
		Services: []application.Service{
			application.NewService(services.NewConnectionService(store, secrets, mgr)),
			application.NewService(services.NewQueryService(mgr)),
			application.NewService(services.NewMetadataService(mgr)),
			application.NewService(services.NewEditService(mgr)),
			application.NewService(services.NewTransferService(mgr)),
			application.NewService(services.NewSyncService(mgr)),
			application.NewService(services.NewSystemService()),
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
		},
		BackgroundColour: application.NewRGB(245, 245, 247),
		URL:              "/",
	})
	wailsbridge.AttachCloseGuard(win)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
