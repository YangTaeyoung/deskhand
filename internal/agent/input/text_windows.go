//go:build windows

package input

import "github.com/YangTaeyoung/deskhand/internal/agent/clipboard"

// windowsClipboard is the clipboard of the agent's session.
type windowsClipboard struct{}

// save copies the clipboard; the copy is put back marked to stay out of
// the clipboard history.
func (windowsClipboard) save() (func() error, error) {
	s, err := clipboard.Save()
	if err != nil {
		return nil, err
	}
	return s.Restore, nil
}

// setText puts text on the clipboard marked to stay out of the clipboard
// history, the cloud clipboard and clipboard monitors.
func (windowsClipboard) setText(text string) error { return clipboard.SetPrivateText(text) }
