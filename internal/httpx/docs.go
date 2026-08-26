package httpx

import (
	"net/http"
	"strconv"

	"gateway/docs"

	"github.com/gin-gonic/gin"
)

// docContentTypes maps embedded doc files to their Content-Type. The Go mime
// package does not map .yaml, so it is set explicitly; everything else is
// inferred by the client from the file extension.
var docContentTypes = map[string]string{
	"openapi.yaml": "text/yaml; charset=utf-8",
}

// serveDocFile serves a single file from the embedded docs package. Only the
// OpenAPI spec and the self-contained Swagger UI assets live there, so this
// never exposes the internal design/deployment documents (ALL-272).
func serveDocFile(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := docs.Content.ReadFile(name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		if ct, ok := docContentTypes[name]; ok {
			c.Header("Content-Type", ct)
		}
		c.Header("Content-Length", strconv.Itoa(len(data)))
		c.Status(http.StatusOK)
		if c.Request.Method != http.MethodHead {
			_, _ = c.Writer.Write(data)
		}
	}
}
