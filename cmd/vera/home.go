package main

import (
	"fmt"

	"github.com/incantery/vera/home"
)

// `vera home`: where her memory is.
//
// One line, so it composes — `cd $(vera home)`, `ls $(vera home)/memory`
// — because the whole point of memory being files is that the ordinary
// tools work on it.
func showHome() error {
	fmt.Println(home.Path(""))
	return nil
}
