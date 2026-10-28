package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

type previewImageJSON struct {
	ID      string `json:"id"`
	Route   string `json:"route"`
	Variant string `json:"variant"`
	Width   int64  `json:"width"`
	Height  int64  `json:"height"`
	URL     string `json:"url"`
}

type previewCaptureJSON struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Error      string             `json:"error"`
	CreatedAt  any                `json:"created_at"`
	FinishedAt any                `json:"finished_at"`
	Images     []previewImageJSON `json:"images"`
}

// ListPreviews returns the task's latest preview screenshot capture, or
// {"capture": null}. Image URLs point at the regular uploads endpoint.
func (h *TasksHandler) ListPreviews(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	c, err := h.q.GetLatestPreviewCapture(r.Context(), taskID)
	if errors.Is(err, sql.ErrNoRows) {
		JSON(w, http.StatusOK, map[string]any{"capture": nil})
		return
	}
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	imgs, err := h.q.ListPreviewImagesByCapture(r.Context(), c.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := previewCaptureJSON{
		ID: c.ID, Status: c.Status, Error: c.Error, CreatedAt: c.CreatedAt, FinishedAt: c.FinishedAt,
		Images: make([]previewImageJSON, 0, len(imgs)),
	}
	for _, im := range imgs {
		out.Images = append(out.Images, previewImageJSON{
			ID: im.ID, Route: im.Route, Variant: im.Variant, Width: im.Width, Height: im.Height,
			URL: "/api/v1/uploads/" + im.TaskID + "/" + filepath.Base(im.Path),
		})
	}
	JSON(w, http.StatusOK, map[string]any{"capture": out})
}
