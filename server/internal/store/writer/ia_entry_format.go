package writer

import "fmt"

func (iaEntryAuthorization) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "iaEntryAuthorization")
}
func (iaCommandEntry) Format(s fmt.State, _ rune) { formatUSPreparation(s, "iaCommandEntry") }
