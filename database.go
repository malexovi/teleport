package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	slutil "github.com/hundredwatt/starlib/util"
	"github.com/hundredwatt/teleport/schema"
	log "github.com/sirupsen/logrus"
	"github.com/xo/dburl"
	"go.starlark.net/starlark"
)

var (
	dbs = make(map[string]*sql.DB)
)

func extractLoadDatabase(sourceOrPath string, destination string, tableName string) {
	var source string
	if strings.Contains(sourceOrPath, "/") {
		source = fileNameWithoutExtension(filepath.Base(sourceOrPath))
	} else {
		source = sourceOrPath
	}

	fnlog := log.WithFields(log.Fields{
		"from":  source,
		"to":    destination,
		"table": tableName,
	})
	fnlog.Info("Starting extract-load")

	var sourceTable schema.Table
	var destinationTable schema.Table
	var columns []schema.Column
	var tableExtract TableExtract
	var csvfile string

	destinationTableName := fmt.Sprintf("%s_%s", source, tableName)

	RunWorkflow([]func() error{
		func() error { return readTableExtractConfiguration(sourceOrPath, tableName, &tableExtract) },
		func() error { return connectDatabaseWithLogging(source) },
		func() error { return connectDatabaseWithLogging(destination) },
		func() error { return inspectTable(source, tableName, &sourceTable, tableExtract.ComputedColumns...) },
		func() error {
			return createDestinationTableIfNotExists(destination, destinationTableName, &sourceTable, &destinationTable)
		},
		func() error {
			return extractSource(&sourceTable, &destinationTable, tableExtract, &columns, &csvfile)
		},
		func() error { return load(&destinationTable, &columns, &csvfile, tableExtract.strategyOpts()) },
	}, func() {
		fnlog.WithField("rows", currentWorkflow.RowCounter).Info("Completed extract-load 🎉")
	})
}

func extractDatabase(sourceOrPath string, tableName string) {
	var source string
	if strings.Contains(sourceOrPath, "/") {
		source = fileNameWithoutExtension(filepath.Base(sourceOrPath))
	} else {
		source = sourceOrPath
	}

	log.WithFields(log.Fields{
		"from":  source,
		"table": tableName,
	}).Info("Extracting table data to CSV")

	var table schema.Table
	var tableExtract TableExtract
	var csvfile string

	RunWorkflow([]func() error{
		func() error { return readTableExtractConfiguration(sourceOrPath, tableName, &tableExtract) },
		func() error { return connectDatabaseWithLogging(source) },
		func() error { return inspectTable(source, tableName, &table, tableExtract.ComputedColumns...) },
		func() error { return extractSource(&table, nil, tableExtract, nil, &csvfile) },
	}, func() {
		log.WithFields(log.Fields{
			"file": csvfile,
			"rows": currentWorkflow.RowCounter,
		}).Info("Extract to CSV completed 🎉")
	})
}

func connectDatabaseWithLogging(source string) (err error) {
	log.WithFields(log.Fields{
		"database": source,
	}).Debug("Establish connection to Database")

	_, err = connectDatabase(source)

	return
}

func inspectTable(source string, tableName string, table *schema.Table, computedColumns ...ComputedColumn) (err error) {
	log.WithFields(log.Fields{
		"database": source,
		"table":    tableName,
	}).Debug("Inspecting Table")

	db, _ := connectDatabase(source)

	dumpedTable, err := schema.DumpTableMetadata(db, tableName)
	if err != nil {
		return
	}
	dumpedTable.Source = source

	for _, computedColumn := range computedColumns {
		column, err := computedColumn.toColumn()
		if err != nil {
			return err
		}
		dumpedTable.Columns = append(dumpedTable.Columns, column)
	}

	*table = *dumpedTable
	return
}

func extractSource(sourceTable *schema.Table, destinationTable *schema.Table, tableExtract TableExtract, columns *[]schema.Column, csvfile *string) (err error) {
	log.WithFields(log.Fields{
		"database": sourceTable.Source,
		"table":    sourceTable.Name,
		"type":     tableExtract.LoadOptions.Strategy,
	}).Debug("Exporting CSV of table data")

	var importColumns []schema.Column
	var exportColumns []schema.Column
	var computedColumns []ComputedColumn

	if destinationTable != nil {
		importColumns = importableColumns(destinationTable, sourceTable)
	} else {
		importColumns = sourceTable.Columns
	}
	for _, column := range importColumns {
		if column.Options[schema.COMPUTED] != 1 {
			exportColumns = append(exportColumns, column)
		} else {
			for _, computedColumn := range tableExtract.ComputedColumns {
				if computedColumn.Name == column.Name {
					computedColumns = append(computedColumns, computedColumn)
					continue
				}
			}
		}
	}

	var whereStatement string
	switch tableExtract.LoadOptions.Strategy {
	case Full:
		whereStatement = ""
	case ModifiedOnly:
		updateTime := (time.Now().Add(time.Duration(-1*tableExtract.LoadOptions.GoBackHours) * time.Hour)).Format("2006-01-02 15:04:05")
		whereStatement = fmt.Sprintf("%s > '%s'", tableExtract.LoadOptions.ModifiedAtColumn, updateTime)
	}

	file, err := exportCSV(sourceTable.Source, sourceTable.Name, exportColumns, whereStatement, tableExtract.ColumnTransforms, tableExtract.ComputedColumns)
	if err != nil {
		return err
	}

	*csvfile = file
	if columns != nil {
		*columns = importColumns
	}
	return
}

func exportCSV(source string, table string, columns []schema.Column, whereStatement string, columnTransforms map[string][]*starlark.Function, computedColumns []ComputedColumn) (string, error) {
	database, err := connectDatabase(source)
	if err != nil {
		return "", fmt.Errorf("Database Open Error: %w", err)
	}

	exists, err := tableExists(source, table)
	if err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("table \"%s\" not found in \"%s\"", table, source)
	}

	columnNames := make([]string, len(columns))
	for i, column := range columns {
		columnNames[i] = column.Name
	}

	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(columnNames, ", "), table)
	if whereStatement != "" {
		query += fmt.Sprintf(" WHERE %s", whereStatement)
	}

	rows, err := database.Query(query)
	if err != nil {
		return "", err
	}

	return generateCSV(columnNames, fmt.Sprintf("extract-%s-%s-*.csv", table, source), func(writer *csv.Writer) error {
		destination := make([]interface{}, len(columnNames))
		rawResult := make([]interface{}, len(columnNames))
		writeBuffer := make([]string, len(columnNames)+len(computedColumns))
		for i := range rawResult {
			destination[i] = &rawResult[i]
		}

		for rows.Next() {
			err := rows.Scan(destination...)
			if err != nil {
				return err
			}

			IncrementRowCounter()

			for i := range columns {
				value, err := applyColumnTransforms(rawResult[i], columnTransforms[columns[i].Name])
				if err != nil {
					return err
				}

				writeBuffer[i] = formatForDatabaseCSV(value, columns[i].DataType)
			}

			for j, computedColumn := range computedColumns {
				i := len(columns) + j

				value, err := computeColumn(rawResult, columns, computedColumn)
				if err != nil {
					return err
				}

				computedColumnColumn, err := computedColumn.toColumn()
				if err != nil {
					return err
				}

				writeBuffer[i] = formatForDatabaseCSV(value, computedColumnColumn.DataType)
			}

			err = writer.Write(writeBuffer)
			if err != nil {
				return err
			}

			if Preview && GetRowCounter() >= int64(PreviewLimit) {
				break
			}
		}

		return nil
	})
}

func connectDatabase(source string) (*sql.DB, error) {
	if dbs[source] != nil {
		return dbs[source], nil
	}

	url := Databases[source].URL
	database, err := dburl.Open(url)
	if err != nil {
		return nil, err
	}

	err = database.Ping()
	if err != nil {
		return nil, err
	}

	dbs[source] = database
	return dbs[source], nil
}

func applyColumnTransforms(value interface{}, columnTransforms []*starlark.Function) (interface{}, error) {
	if len(columnTransforms) == 0 {
		return value, nil
	}

	slvalue, err := slutil.Marshal(value)
	if err != nil {
		return "", err
	}

	for _, function := range columnTransforms {
		slvalue, err = starlark.Call(GetThread(), function, starlark.Tuple{slvalue}, nil)
		if err != nil {
			return "", err
		}
	}

	value, err = slutil.Unmarshal(slvalue)
	if err != nil {
		return "", err
	}

	return value, nil
}

func computeColumn(rawResult []interface{}, columns []schema.Column, computedColumn ComputedColumn) (interface{}, error) {
	row := make(map[string]interface{})
	for i := range columns {
		row[columns[i].Name] = rawResult[i]
	}

	arg, err := slutil.Marshal(row)
	if err != nil {
		return "", err
	}

	slvalue, err := starlark.Call(GetThread(), computedColumn.Function, starlark.Tuple{arg}, nil)
	if err != nil {
		return "", err
	}

	value, err := slutil.Unmarshal(slvalue)
	if err != nil {
		return "", err
	}

	return value, nil
}

func formatForDatabaseCSV(value interface{}, dataType schema.DataType) string {
	if dataType == schema.DATE {
		switch value.(type) {
		case string:
			return value.(string)
		case time.Time:
			return value.(time.Time).Format("2006-01-02")
		}
	}

	return formatForCSV(value)
}
