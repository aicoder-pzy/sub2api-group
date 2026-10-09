package admin

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type PelicanAssessmentHandler struct {
	svc *service.PelicanAssessmentService
}

func NewPelicanAssessmentHandler(svc *service.PelicanAssessmentService) *PelicanAssessmentHandler {
	return &PelicanAssessmentHandler{svc: svc}
}

func pelicanAssessmentHTML(c *gin.Context) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		response.Error(c, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return "", false
	}
	var input struct {
		HTML string `json:"html"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 6*service.PelicanAssessmentMaxHTMLBytes+1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "only an HTML field is accepted; credentials and generation parameters are prohibited")
		return "", false
	}
	return input.HTML, true
}

func (h *PelicanAssessmentHandler) Start(c *gin.Context) {
	html, ok := pelicanAssessmentHTML(c)
	if !ok {
		return
	}
	record, err := h.svc.Start(c.Request.Context(), html)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, record)
}

// Lookup reads our database only. Opening a preview never uploads to Manxue.
func (h *PelicanAssessmentHandler) Lookup(c *gin.Context) {
	html, ok := pelicanAssessmentHTML(c)
	if !ok {
		return
	}
	record, err := h.svc.Lookup(c.Request.Context(), html)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, record)
}

func (h *PelicanAssessmentHandler) Poll(c *gin.Context) {
	record, err := h.svc.Poll(c.Request.Context(), c.Param("hash"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, record)
}
