// dummy contains code used in tests.
package dummy

import "fmt"

func ExportedFunction() {
	fmt.Print("hi")
}

var ExportedVariable = 10
