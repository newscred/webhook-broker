//+build wireinject

package main

import (
	"github.com/google/wire"
)

// InitializeDBEngineName Creates DBEngineName
func InitializeDBEngineName() DBEngineName {
	wire.Build(NewDBEngineName)
	return "Test"
}
