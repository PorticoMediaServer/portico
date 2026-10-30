// devhash prints a bcrypt hash for a development password. Not shipped.
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	h, e := bcrypt.GenerateFromPassword([]byte(os.Args[1]), 10)
	if e != nil {
		panic(e)
	}
	fmt.Print(string(h))
}
