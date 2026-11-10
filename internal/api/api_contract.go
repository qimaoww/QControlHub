package api

import (
	"archive/zip"
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed contract/openapi.json management-skill/qcontrolhub-manager
var apiProductFiles embed.FS

func (s *Server) apiContractRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/openapi.json", s.requireAllPermissions(nil, http.HandlerFunc(s.getAPIContract)))
	mux.Handle("GET /api/v1/management-skill", s.requireAllPermissions(nil, http.HandlerFunc(s.downloadManagementSkill)))
}
func (s *Server) getAPIContract(w http.ResponseWriter, r *http.Request) {
	data, err := apiProductFiles.ReadFile("contract/openapi.json")
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}
func (s *Server) downloadManagementSkill(w http.ResponseWriter, r *http.Request) {
	var b bytes.Buffer
	archive := zip.NewWriter(&b)
	err := fs.WalkDir(apiProductFiles, "management-skill", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := apiProductFiles.ReadFile(path)
		if err != nil {
			return err
		}
		entry, err := archive.Create(strings.TrimPrefix(path, "management-skill/"))
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	})
	if err == nil {
		err = archive.Close()
	}
	if err != nil {
		writeInternalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="qcontrolhub-manager.zip"`)
	_, _ = w.Write(b.Bytes())
}
