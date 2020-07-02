package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/hundredwatt/teleport/schema"
	"gopkg.in/yaml.v2"
)

func tableExists(source string, tableName string) (bool, error) {
	database, err := connectDatabase(source)
	if err != nil {
		return false, err
	}

	tables, err := schema.TableNames(database)
	if err != nil {
		return false, err
	}

	for _, table := range tables {
		if table == tableName {
			return true, nil
		}
	}

	return false, nil
}

func createTable(database *sql.DB, tableName string, table *schema.Table) error {
	statement := table.GenerateCreateTableStatement(tableName)

	_, err := database.Exec(statement)

	return err
}

func listTables(source string) {
	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Open Error:", err)
	}

	tables, err := schema.TableNames(database)
	if err != nil {
		log.Fatal("Database Error:", err)
	}
	for _, tablename := range tables {
		fmt.Println(tablename)
	}
}

func dropTable(source string, table string) {
	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Open Error:", err)
	}

	exists, err := tableExists(source, table)
	if err != nil {
		log.Fatal(err)
	} else if !exists {
		log.Fatalf("table \"%s\" not found in \"%s\"", table, source)
	}

	_, err = database.Exec(fmt.Sprintf("DROP TABLE %s", table))
	if err != nil {
		log.Fatal(err)
	}
}

func createDestinationTable(source string, destination string, sourceTableName string) {
	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Open Error:", err)
	}

	table, err := schema.DumpTableMetadata(database, sourceTableName)
	if err != nil {
		log.Fatal("schema.Table Metadata Error:", err)
	}

	destinationDatabase, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Connect Error:", err)
	}

	err = createTable(destinationDatabase, fmt.Sprintf("%s_%s", source, sourceTableName), table)

	if err != nil {
		log.Fatal(err)
	}
}

func createDestinationTableFromConfigFile(source string, file string) {
	table := readTableFromConfigFile(file)

	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Connect Error:", err)
	}

	statement := table.GenerateCreateTableStatement(fmt.Sprintf("%s_%s", table.Source, table.Name))

	_, err = database.Exec(statement)
	if err != nil {
		log.Fatal(err)
	}
}

func aboutDB(source string) {
	fmt.Println("Name: ", source)
	fmt.Printf("Type: %s\n", GetDialect(Databases[source]).HumanName)
}

func databaseTerminal(source string) {
	command := GetDialect(Databases[source]).TerminalCommand
	if command == "" {
		log.Fatalf("Not implemented for this database type")
	}

	binary, err := exec.LookPath(command)
	if err != nil {
		log.Fatalf("command exec err (%s): %s", command, err)
	}

	env := os.Environ()

	err = syscall.Exec(binary, []string{command, Databases[source].URL}, env)
	if err != nil {
		log.Fatalf("Syscall error: %s", err)
	}

}

func describeTable(source string, tableName string) {
	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Open Error:", err)
	}

	table, err := schema.DumpTableMetadata(database, tableName)
	if err != nil {
		log.Fatal("Describe schema.Table Error:", err)
	}

	fmt.Println("Source: ", table.Source)
	fmt.Println("schema.Table: ", table.Name)
	fmt.Println()
	fmt.Println("schema.Columns:")
	fmt.Println("========")
	for _, column := range table.Columns {
		fmt.Print(column.Name, " | ", column.DataType)
		if len(column.Options) > 0 {
			fmt.Print(" ( ")
			for option, value := range column.Options {
				fmt.Print(option, ": ", value, ", ")

			}
			fmt.Print(" )")
		}
		fmt.Println()
	}
}

func tableMetadata(source string, tableName string) {
	database, err := connectDatabase(source)
	if err != nil {
		log.Fatal("Database Open Error:", err)
	}

	table, err := schema.DumpTableMetadata(database, tableName)
	if err != nil {
		log.Fatal("Describe schema.Table Error:", err)
	}

	b, err := yaml.Marshal(table)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(b))
}

func readTableFromConfigFile(file string) *schema.Table {
	var table schema.Table

	yamlFile, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatalf("yamlFile.Get err   #%v ", err)
	}

	err = yaml.Unmarshal(yamlFile, &table)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}

	return &table
}