// Command yapuno-pushkey creates the private VAPID key with which yapuno-web
// identifies itself to push services. It writes a new file with mode 0600,
// never overwrites one, and prints only the public key. It is an offline
// administrator tool; see docs/adr/draft-web-push.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pblumer/yapuno/internal/webpush"
)

func run(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: yapuno-pushkey <new key file>")
	}
	path := args[0]
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("directory %s must exist and have mode 0700", dir)
	}
	if err := webpush.GenerateKeyFile(path); err != nil {
		return fmt.Errorf("key not created: %w", err)
	}
	// Any HTTPS origin will do for reading the key back.
	key, err := webpush.LoadKey(path, "https://localhost")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, key.PublicKey())
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
