package main

import "errors"

// runMac is not available on Windows.
func runMac([]string) error { return errors.New("mac mode runs on the Mac only") }
