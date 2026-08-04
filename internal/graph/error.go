package graph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

var (
	ErrItemNotFound  = errors.New("Item no encontrado")
	ErrInvalidToken  = errors.New("Invalid token")
	ErrEmptyName     = errors.New("The name cannot be empty")
	ErrEmptyResource = errors.New("The resource cannot be empty")
	ErrNilContent    = errors.New("El contenido no puede ser nil")
	ErrInvalidName   = errors.New("Invalid item name")
	ErrThrottled     = errors.New("Demasiadas peticiones a la API") // nuevo (429)
	ErrConflict      = errors.New("Conflicto al modificar")         // nuevo (409)
)

// GraphError representa un error devuelto por Microsoft Graph API
type GraphError struct {
	StatusCode int    // HTTP code (e.g.: 404, 401)
	Code       string `json:"code"`    // Graph error code (e.g.: "itemNotFound")
	Message    string `json:"message"` // Mensaje descriptivo del error
}

func (e *GraphError) Error() string {
	return fmt.Sprintf("graph error HTTP %d: [%s] %s", e.StatusCode, e.Code, e.Message)
}

// Is allows using errors.Is to check sentinel errors by HTTP code.
func (e *GraphError) Is(target error) bool {
	if target == ErrItemNotFound {
		return e.StatusCode == http.StatusNotFound
	}
	if target == ErrInvalidToken {
		return e.StatusCode == http.StatusUnauthorized
	}
	if target == ErrThrottled {
		return e.StatusCode == http.StatusTooManyRequests
	}
	if target == ErrConflict {
		return e.StatusCode == http.StatusConflict
	}
	return false
}

// checkResponse valida el status HTTP y parsea errores de Microsoft Graph.
//
// Comportamiento:
//   - If the status is 2xx (success): returns nil WITHOUT consuming the body
//   - Si el status es error (4xx, 5xx): CONSUME el body para parsear el error de Graph
//
// Importante: El caller debe usar defer resp.Body.Close() siempre.
// If checkResponse returns an error, the body was ALREADY consumed (but must still be closed with defer).
// If checkResponse returns nil, the body is INTACT and ready to be read.
func checkResponse(resp *http.Response) error {
	// Caso exitoso: NO tocamos el body para que el caller pueda leerlo
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Caso de error: CONSUMIMOS el body para obtener detalles del error
	// Read with a limit for security (avoid huge malicious responses)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<10)) // Max 10KB
	if err != nil {
		return &GraphError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("error leyendo cuerpo de respuesta: %v", err),
		}
	}

	// Intentamos parsear el JSON de error de Graph
	var errResp struct {
		Error *GraphError `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err != nil {
		// If not valid JSON, return the raw body (truncated if too long)
		bodyStr := string(body)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return &GraphError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("respuesta no JSON: %s", bodyStr),
		}
	}

	if errResp.Error != nil {
		errResp.Error.StatusCode = resp.StatusCode
		return errResp.Error
	}

	// Valid JSON response but without the "error" field
	return &GraphError{
		StatusCode: resp.StatusCode,
		Message:    "error desconocido de Graph (JSON sin campo 'error')",
	}
}
