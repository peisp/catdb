package mysqldrv

import (
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"

	"catdb/internal/dbdriver"
)

// buildDSN constructs the go-sql-driver DSN from a ConnConfig.
//
// network is "tcp" for direct connections and a previously-registered name
// (via mysql.RegisterDialContext) when an SSH tunnel is in use.
// tlsName is "" when SSL is disabled or the name registered with
// mysql.RegisterTLSConfig (or one of the built-in "true"/"skip-verify" values).
func buildDSN(cfg dbdriver.ConnConfig, network, tlsName string) string {
	port := cfg.Port
	if port == 0 {
		port = 3306
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, port)

	c := mysql.NewConfig()
	c.User = cfg.User
	c.Passwd = cfg.Password
	c.Net = network
	if c.Net == "" {
		c.Net = "tcp"
	}
	c.Addr = addr
	c.DBName = cfg.Database
	c.AllowNativePasswords = true
	c.ParseTime = true
	c.Loc = parseLocation(cfg.Params["loc"])
	c.Collation = stringDefault(cfg.Params["collation"], "utf8mb4_general_ci")

	if v, ok := cfg.Params["timeout"]; ok {
		c.Timeout = parseDurationDefault(v, 0)
	} else {
		c.Timeout = defaultDialTimeout
	}
	if v, ok := cfg.Params["readTimeout"]; ok {
		c.ReadTimeout = parseDurationDefault(v, 0)
	}
	if v, ok := cfg.Params["writeTimeout"]; ok {
		c.WriteTimeout = parseDurationDefault(v, 0)
	}
	if v, ok := cfg.Params["maxAllowedPacket"]; ok {
		c.MaxAllowedPacket = parseIntDefault(v, 0)
	}

	if tlsName != "" {
		c.TLSConfig = tlsName
	}

	// Pass anything else the user wrote into Params straight through as DSN
	// query parameters — keeps the door open without us having to mirror every
	// mysql driver option.
	c.Params = map[string]string{}
	for k, v := range cfg.Params {
		switch k {
		case "loc", "collation", "timeout", "readTimeout", "writeTimeout", "maxAllowedPacket":
			continue
		default:
			c.Params[k] = v
		}
	}

	return c.FormatDSN()
}

func stringDefault(v, def string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	return v
}
