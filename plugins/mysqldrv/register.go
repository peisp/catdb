//go:build !no_mysql

package mysqldrv

import "catdb/internal/registry"

// Registration lives in its own tagged file: mariadbdrv imports this package
// for its implementation, so `-tags no_mysql` must strip the "mysql" registry
// entry without stripping the shared code.
func init() { registry.Register(Driver{}) }
