// +build redshift

package schema

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
)

func TestRedshiftInspection(t *testing.T) {
	withDb(t, os.ExpandEnv("redshift://$TEST_REDSHIFT_USER:$TEST_REDSHIFT_PASSWORD@$TEST_REDSHIFT_HOST:5439/dev"), func(db *sql.DB) {
		testColumnCases(t, db, genericCases)

		testColumnCases(t, db, genericStringCases)

		// Special Types
		testColumnCases(t, db, []struct {
			originalDataTypes  []string
			column             Column
			createTabeDataType string
		}{
			{
				[]string{"text"},
				Column{"", STRING, map[Option]int{LENGTH: 256}},
				"VARCHAR(256)",
			},
		})
	})
}

func TestRedshiftTableGeneration(t *testing.T) {
	withDb(t, os.ExpandEnv("redshift://$TEST_REDSHIFT_USER:$TEST_REDSHIFT_PASSWORD@$TEST_REDSHIFT_HOST:5439/dev"), func(db *sql.DB) {
		_, err := db.Exec(widgetsTable.GenerateRedshiftCreateTableStatement("new_widgets"))
		if err != nil {
			assert.FailNow(t, err.Error())
		}
		defer db.Exec(`DROP TABLE new_widgets`)

		table, err := DumpTableMetadata(db, "new_widgets")
		if err != nil {
			assert.FailNow(t, err.Error())
		}

		for idx, widgetsColumn := range widgetsTable.Columns {
			dumpedColumn := table.Columns[idx]

			switch widgetsColumn.DataType {
			case TEXT:
				assert.Equal(t, widgetsColumn.Name, dumpedColumn.Name)
				assert.Equal(t, STRING, dumpedColumn.DataType)
				assert.Equal(t, map[Option]int{LENGTH: 65535}, dumpedColumn.Options)
			default:
				assert.Equal(t, widgetsColumn.Name, dumpedColumn.Name)
				assert.Equal(t, widgetsColumn.DataType, dumpedColumn.DataType)
				assert.Equal(t, widgetsColumn.Options, dumpedColumn.Options)
			}
		}
	})
}

func TestRedshiftAddColumn(t *testing.T) {
	withDb(t, os.ExpandEnv("redshift://$TEST_REDSHIFT_USER:$TEST_REDSHIFT_PASSWORD@$TEST_REDSHIFT_HOST:5439/dev"), func(db *sql.DB) {
		testAddColumn(t, db)
	})
}
