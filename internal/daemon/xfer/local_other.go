//go:build !darwin

package xfer

// setHidden leaves the entry path as it is: hidden part files are a macOS
// flag.
func setHidden(string, bool) error { return nil }

// renameNoReplace renames from to the path to, which must not exist; when
// to exists the error matches fs.ErrExist.
func renameNoReplace(from, to string) error { return linkRename(from, to) }

// writable returns nil; the removal itself reports a folder that does not
// let this process remove its entries.
func writable(string) error { return nil }

// caseInsensitive reports false: the volumes are taken as telling letter
// case apart.
func caseInsensitive(string) bool { return false }
