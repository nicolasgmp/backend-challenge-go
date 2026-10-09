package main

import (
	"os"

	"go.uber.org/fx"

	"jungle-gaming-challeng/internal/bootstrap"
)

func main() {
	fx.New(bootstrap.Options(os.Getenv, os.Stdout)).Run()
}
