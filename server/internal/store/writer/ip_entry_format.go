package writer

import "fmt"

func (ipEntryAuthorization) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEntryAuthorization")
}
func (ipCommandEntry) Format(s fmt.State, _ rune) { formatUSPreparation(s, "ipCommandEntry") }
