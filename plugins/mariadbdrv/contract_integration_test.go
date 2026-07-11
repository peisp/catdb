//go:build integration

// Integration test: spins a real MariaDB via testcontainers and runs the shared
// dbdriver contract suite against mariadbdrv.
//
// Run locally:
//
//	go test -tags=integration ./plugins/mariadbdrv/...
//
// Requires Docker (testcontainers connects to the local daemon).
package mariadbdrv

import (
	"context"
	"fmt"
	"testing"
	"time"

	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"

	"catdb/internal/dbdriver"
	"catdb/internal/dbdriver/contract"
)

func TestMariaDBContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := tcmariadb.Run(ctx, "mariadb:11",
		tcmariadb.WithDatabase("test"),
		tcmariadb.WithUsername("root"),
		tcmariadb.WithPassword("secret"),
	)
	if err != nil {
		t.Fatalf("start mariadb container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	cfg := dbdriver.ConnConfig{
		Host:     host,
		Port:     int(port.Num()),
		User:     "root",
		Password: "secret",
		Database: "test",
	}

	contract.Run(t, ctx, driver{}, cfg, contract.Fixtures{
		SleepSQL: "SELECT SLEEP(2)",
		CreateTableSQL: func(qualified string) string {
			return fmt.Sprintf(`CREATE TABLE %s (
				id INT NOT NULL PRIMARY KEY AUTO_INCREMENT,
				name VARCHAR(64) NOT NULL,
				created_at DATETIME NULL
			)`, qualified)
		},
	})
}
