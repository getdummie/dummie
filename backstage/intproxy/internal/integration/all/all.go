// Package all registers the integrations this binary is built with. Importing
// it is what makes them known to config validation and to the server, so the
// core never has to name any of them.
package all

import (
	_ "intproxy/internal/integration/github"
)
