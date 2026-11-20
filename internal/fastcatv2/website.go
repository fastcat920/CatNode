package fastcatv2

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

func openWebsite(dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open website root: %w", err)
	}
	info, err := root.Stat("index.html")
	if err != nil || !info.Mode().IsRegular() {
		root.Close()
		return nil, fmt.Errorf("website root requires a regular index.html")
	}
	return root, nil
}

func websiteHandler(root *os.Root, tunnel http.Handler, altSvc string) http.Handler {
	if root == nil {
		return tunnel
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			tunnel.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		file, info, err := openPublicFile(root, r.URL.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		if altSvc != "" {
			w.Header().Set("Alt-Svc", altSvc)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, path.Base(file.Name()), info.ModTime(), file)
	})
}

func openPublicFile(root *os.Root, requestPath string) (*os.File, fs.FileInfo, error) {
	name := strings.TrimPrefix(requestPath, "/")
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	if !fs.ValidPath(name) {
		return nil, nil, fs.ErrNotExist
	}
	for _, component := range strings.Split(name, "/") {
		if strings.HasPrefix(component, ".") {
			return nil, nil, fs.ErrNotExist
		}
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, fs.ErrNotExist
	}
	return file, info, nil
}
