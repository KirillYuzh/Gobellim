// Command kitctl manages local models and validates policy files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/embed"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "policy":
		os.Exit(runPolicy(os.Args[2:]))
	case "model":
		os.Exit(runModel(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "kitctl: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `kitctl commands:
  policy check <file>                 validate policy.yaml in strict mode
  model pull <org/name> [--dir DIR]   download model files into ./models
`)
}

func runPolicy(args []string) int {

	rest := args
	if len(rest) > 0 && rest[0] == "check" {
		rest = rest[1:]
	}
	fs := flag.NewFlagSet("policy", flag.ContinueOnError)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "kitctl policy: expected exactly one file")
		return 2
	}
	path := fs.Arg(0)
	if _, err := authz.Load(path); err != nil {
		fmt.Fprintf(os.Stderr, "kitctl policy: %v\n", err)
		return 1
	}
	fmt.Printf("policy %s: ok\n", path)
	return 0
}

var modelFiles = []string{"model.onnx", "tokenizer.json", "config.json"}

func runModel(args []string) int {
	if len(args) < 1 || args[0] != "pull" {
		fmt.Fprintln(os.Stderr, "kitctl model: expected 'pull <org/name>'")
		return 2
	}
	fs := flag.NewFlagSet("model pull", flag.ContinueOnError)
	dir := fs.String("dir", defaultModelsDir(), "models directory")
	baseURL := fs.String("base-url", "https://huggingface.co", "model host")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "kitctl model pull: expected a model name like org/name")
		return 2
	}
	modelName := fs.Arg(0)
	dest := embed.ModelDir(*dir, modelName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	hc := &http.Client{Timeout: 30 * time.Minute}
	for _, name := range modelFiles {
		url := fmt.Sprintf("%s/%s/resolve/main/%s", *baseURL, modelName, name)
		if err := download(ctx, hc, url, filepath.Join(dest, name)); err != nil {
			fmt.Fprintf(os.Stderr, "kitctl model pull: %v\n", err)
			return 1
		}
		fmt.Printf("fetched %s\n", name)
	}
	fmt.Printf("model %s ready in %s\n", modelName, dest)
	return 0
}

func defaultModelsDir() string {
	if v := os.Getenv("KIT_MODELS_DIR"); v != "" {
		return v
	}
	return "./models"
}

func download(ctx context.Context, hc *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := errors.Join(f.Close(), os.Rename(tmp, path)); err != nil {
		return err
	}
	return nil
}
