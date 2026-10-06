//go:build !windows

package main

func showError(message string) { printError(message) }
