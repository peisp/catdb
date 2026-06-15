// catdb — a cross-platform database management tool built on Wails v3.
//
// IMPORTANT: per CLAUDE.md #1, this file is the ONLY place outside
// wailsbridge/ that may import github.com/wailsapp/wails/v3/pkg/application
// directly. Everything else routes through wailsbridge.
package main

import (
	"embed"
	"log"

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

	app := application.New(application.Options{
		Name:        "catdb",
		Description: "Cross-platform database management tool",
		Services: []application.Service{
			application.NewService(services.NewConnectionService(store, secrets, mgr)),
			application.NewService(services.NewQueryService(mgr)),
			application.NewService(services.NewMetadataService(mgr)),
			application.NewService(services.NewEditService(mgr)),
			application.NewService(services.NewTransferService(mgr)),
			application.NewService(services.NewSystemService()),
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
	app.Event.On("system:switch-english-input", func(_ *application.CustomEvent) {
		platform.SwitchToEnglishInputSource()
	})
	app.Menu.SetApplicationMenu(wailsbridge.BuildApplicationMenu(app))

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "catdb",
		Width:  1200,
		Height: 760,
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
