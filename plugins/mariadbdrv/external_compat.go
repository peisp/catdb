package mariadbdrv

import (
	"catdb/internal/dbdriver"
	"catdb/plugins/mysqldrv"
)

// MariaDB's JDBC vocabulary is Connector/J's, so the embedded mysqldrv.Driver
// already provides TranslateExternalProp. Only the identity bits differ — the
// alias list and the URL scheme — exactly like Name()/UIDialect() above.

// JDBCPrefix is the MariaDB Connector/J URL scheme.
const JDBCPrefix = "jdbc:mariadb://"

// ExternalDriverAliases lists the DBeaver driver ids that mean "MariaDB".
func (driver) ExternalDriverAliases() []string { return []string{"mariadb"} }

// ParseExternalURL parses `jdbc:mariadb://host[:port]/[db][?k=v&…]`.
func (driver) ParseExternalURL(externalURL string) (dbdriver.ExternalURLParts, error) {
	return mysqldrv.ParseExternalURLWithPrefix(externalURL, JDBCPrefix)
}
