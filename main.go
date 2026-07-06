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

	"catdb/internal/core/session"
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
}

func main() {
	store, err := storage.Open("")
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	defer store.Close()

	secrets := storage.NewSecrets("catdb")
	mgr := session.NewManager(store, secrets)
	defer mgr.CloseAll()

	settingsSvc := services.NewSettingsService(store)

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
