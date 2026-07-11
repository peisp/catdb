// Package mariadbdrv registers the MariaDB driver. MariaDB speaks the MySQL
// wire protocol and shares information_schema semantics, so the driver embeds
// mysqldrv.Driver and overrides only its identity and editor dialect id.
package mariadbdrv

import (
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/plugins/mysqldrv"
)

func init() { registry.Register(driver{}) }

type driver struct{ mysqldrv.Driver }

func (driver) Name() string { return "mariadb" }

// UIDialect reuses the MySQL descriptor with the CodeMirror MariaSQL dialect.
func (d driver) UIDialect() dbdriver.UIDialect {
	ui := d.Driver.UIDialect()
	ui.EditorDialect = "mariadb"
	return ui
}
