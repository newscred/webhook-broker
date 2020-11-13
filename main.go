package main

import (
	"fmt"
)

// DBEngineName is string alias for DB Engine Name
type DBEngineName string

// NewDBEngineName creates new DB Engine Name
func NewDBEngineName() DBEngineName {
	return "mysql"
}

func main() {
	engineName := InitializeDBEngineName()

	fmt.Println(engineName)
}
