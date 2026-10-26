package sifende

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrorAPI es una respuesta de error de la API. Casi siempre trae un Problem Details (RFC 9457) con el código del
// error.
type ErrorAPI struct {
	Status int
	// Codigo es el último segmento del type del Problem Details: "validation-error", "document-quota-exceeded"…
	Codigo string
	// Problema es nil si el cuerpo no es un Problem Details.
	Problema *ProblemDetail
	// Cuerpo es la respuesta tal como llegó, para diagnosticar errores que no son Problem Details.
	Cuerpo  []byte
	TraceID string
	// ReintentarEn es el Retry-After de la respuesta, si vino.
	ReintentarEn *time.Duration
	// IdempotencyKey es la clave con la que se envió la operación: repetila para recuperar el resultado.
	IdempotencyKey string
}

func (e *ErrorAPI) Error() string {
	if p := e.Problema; p != nil {
		mensaje := fmt.Sprintf("%d %s", e.Status, p.Title)
		if p.Detail != "" {
			mensaje += ": " + p.Detail
		}
		return "sifende: " + strings.TrimSpace(mensaje)
	}
	var cuerpo struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(e.Cuerpo, &cuerpo) == nil && cuerpo.Error != nil {
		return fmt.Sprintf("sifende: %d %v", e.Status, cuerpo.Error)
	}
	return fmt.Sprintf("sifende: la API respondió %d", e.Status)
}

// Errores devuelve los errores de validación por campo de un validation-error, como
// {"receptor.digitoVerificador": "…"}.
func (e *ErrorAPI) Errores() map[string]string {
	if e.Problema == nil || e.Problema.Errores == nil {
		return map[string]string{}
	}
	return e.Problema.Errores
}

func nuevoErrorAPI(status int, cuerpo []byte, headers http.Header, clave string) *ErrorAPI {
	e := &ErrorAPI{
		Status:         status,
		Cuerpo:         cuerpo,
		Problema:       leerProblema(cuerpo),
		TraceID:        headers.Get("X-Trace-Id"),
		ReintentarEn:   leerRetryAfter(headers.Get("Retry-After"), time.Now()),
		IdempotencyKey: clave,
	}
	if e.Problema != nil {
		segmentos := strings.FieldsFunc(e.Problema.Type, func(r rune) bool { return r == '/' })
		if len(segmentos) > 0 {
			e.Codigo = segmentos[len(segmentos)-1]
		}
		if e.Problema.TraceID != "" {
			e.TraceID = e.Problema.TraceID
		}
	}
	return e
}

// leerProblema sólo reconoce un Problem Details si los campos que el SDK lee tienen el tipo esperado.
func leerProblema(cuerpo []byte) *ProblemDetail {
	var campos map[string]json.RawMessage
	if json.Unmarshal(cuerpo, &campos) != nil {
		return nil
	}
	_, tieneType := campos["type"]
	_, tieneTitle := campos["title"]
	_, tieneDetail := campos["detail"]
	if !tieneType && !tieneTitle && !tieneDetail {
		return nil
	}
	var p ProblemDetail
	textos := []struct {
		campo   string
		destino *string
	}{{"type", &p.Type}, {"title", &p.Title}, {"detail", &p.Detail}, {"traceId", &p.TraceID}}
	for _, t := range textos {
		if crudo, ok := campos[t.campo]; ok && json.Unmarshal(crudo, t.destino) != nil {
			return nil
		}
	}
	if crudo, ok := campos["errores"]; ok && json.Unmarshal(crudo, &p.Errores) != nil {
		return nil
	}
	// El resto es informativo: un campo con otro tipo no invalida el Problem Details.
	_ = json.Unmarshal(campos["status"], &p.Status)
	_ = json.Unmarshal(campos["instance"], &p.Instance)
	_ = json.Unmarshal(campos["campo"], &p.Campo)
	_ = json.Unmarshal(campos["valorRecibido"], &p.ValorRecibido)
	_ = json.Unmarshal(campos["valoresAceptados"], &p.ValoresAceptados)
	return &p
}

func leerRetryAfter(valor string, ahora time.Time) *time.Duration {
	valor = strings.TrimSpace(valor)
	if valor == "" {
		return nil
	}
	if segundos, err := strconv.ParseFloat(valor, 64); err == nil {
		espera := time.Duration(max(0, segundos) * float64(time.Second))
		return &espera
	}
	fecha, err := http.ParseTime(valor)
	if err != nil {
		return nil
	}
	espera := max(0, fecha.Sub(ahora))
	return &espera
}

// ErrorConexion indica que no hubo una respuesta completa de la API: falló la conexión, venció el timeout o el cuerpo
// llegó cortado o ilegible. La operación pudo haberse ejecutado: repetila con la misma IdempotencyKey.
type ErrorConexion struct {
	Mensaje        string
	IdempotencyKey string
	// Timeout es true si el intento venció el timeout del cliente, incluso a mitad de la respuesta.
	Timeout bool
	Err     error
}

func (e *ErrorConexion) Error() string {
	if e.Err == nil {
		return "sifende: " + e.Mensaje
	}
	return "sifende: " + e.Mensaje + ": " + e.Err.Error()
}

func (e *ErrorConexion) Unwrap() error { return e.Err }

// ErrorKudePendiente indica que el KuDE no estuvo listo dentro de la espera de DescargarKude.
type ErrorKudePendiente struct {
	CDC string
}

func (e *ErrorKudePendiente) Error() string {
	return fmt.Sprintf("sifende: el KuDE de %s sigue en preparación", e.CDC)
}

// ErrFirmaWebhookInvalida envuelve todo rechazo de VerificarWebhook: firma distinta, headers faltantes o timestamp
// fuera de la tolerancia. Respondé 401 y no proceses el evento.
var ErrFirmaWebhookInvalida = errors.New("sifende: firma de webhook inválida")
