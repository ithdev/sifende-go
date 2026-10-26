package sifende

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ToleranciaWebhookPorDefecto es la diferencia máxima aceptada entre X-Sifende-Timestamp y el reloj local.
const ToleranciaWebhookPorDefecto = 5 * time.Minute

// EventoWebhook es una entrega verificada. Data depende de Tipo: leela con Documento, Cancelacion o Lote.
//
// Una entrega puede repetirse: deduplicá por ID.
type EventoWebhook struct {
	ID              string            `json:"id"`
	Tipo            TipoEventoWebhook `json:"tipo"`
	Version         string            `json:"version"`
	OcurridoEn      string            `json:"ocurridoEn"`
	ContribuyenteID int64             `json:"contribuyenteId"`
	// Ambiente puede faltar en entregas creadas antes de que se agregara el campo.
	Ambiente Ambiente        `json:"ambiente,omitempty"`
	Data     json.RawMessage `json:"data"`
}

type ResultadoSifen struct {
	Codigo  string `json:"codigo"`
	Mensaje string `json:"mensaje"`
}

// DatosDocumento es el Data de documento.aprobado y documento.rechazado.
type DatosDocumento struct {
	DocumentoID string `json:"documentoId"`
	CDC         string `json:"cdc"`
	Estado      string `json:"estado"`
	// LoteID es nil fuera de un lote, y siempre nil dentro de DatosLote.Documentos.
	LoteID                *int64           `json:"loteId"`
	ProtocoloAutorizacion *string          `json:"protocoloAutorizacion"`
	Resultados            []ResultadoSifen `json:"resultados"`
}

// DatosCancelacion es el Data de documento.cancelado.
type DatosCancelacion struct {
	DocumentoID           string  `json:"documentoId"`
	CDC                   string  `json:"cdc"`
	Estado                string  `json:"estado"`
	EventoSifenID         int64   `json:"eventoSifenId"`
	Motivo                string  `json:"motivo"`
	CodigoRespuesta       *string `json:"codigoRespuesta"`
	MensajeRespuesta      *string `json:"mensajeRespuesta"`
	ProtocoloAutorizacion *string `json:"protocoloAutorizacion"`
}

// DatosLote es el Data de lote.procesado.
type DatosLote struct {
	LoteID                  int64            `json:"loteId"`
	NumeroLote              string           `json:"numeroLote"`
	Estado                  string           `json:"estado"`
	Aprobados               int              `json:"aprobados"`
	AprobadosConObservacion int              `json:"aprobadosConObservacion"`
	Rechazados              int              `json:"rechazados"`
	Documentos              []DatosDocumento `json:"documentos"`
}

// Documento devuelve el Data de documento.aprobado o documento.rechazado.
func (e *EventoWebhook) Documento() (*DatosDocumento, error) {
	var d DatosDocumento
	return &d, e.datos(&d, TipoEventoWebhookDocumentoAprobado, TipoEventoWebhookDocumentoRechazado)
}

// Cancelacion devuelve el Data de documento.cancelado.
func (e *EventoWebhook) Cancelacion() (*DatosCancelacion, error) {
	var d DatosCancelacion
	return &d, e.datos(&d, TipoEventoWebhookDocumentoCancelado)
}

// Lote devuelve el Data de lote.procesado.
func (e *EventoWebhook) Lote() (*DatosLote, error) {
	var d DatosLote
	return &d, e.datos(&d, TipoEventoWebhookLoteProcesado)
}

func (e *EventoWebhook) datos(destino any, tipos ...TipoEventoWebhook) error {
	for _, t := range tipos {
		if e.Tipo == t {
			if err := json.Unmarshal(e.Data, destino); err != nil {
				return fmt.Errorf("sifende: el data de %s no tiene la forma esperada: %w", e.Tipo, err)
			}
			return nil
		}
	}
	return fmt.Errorf("sifende: un evento %s no trae ese data", e.Tipo)
}

// OpcionesVerificacion configura VerificarWebhook.
type OpcionesVerificacion struct {
	// Cuerpo es el cuerpo crudo tal como llegó: la firma se calcula sobre bytes, no sobre el JSON reserializado.
	Cuerpo  []byte
	Headers http.Header
	// Secretos son los secretos whsec_… del endpoint. Durante una rotación, pasá el nuevo y el anterior.
	Secretos []string
	// Tolerancia es la antigüedad máxima de X-Sifende-Timestamp. Cero es ToleranciaWebhookPorDefecto.
	Tolerancia time.Duration
	// Ahora reemplaza el reloj, para tests. Cero es time.Now().
	Ahora time.Time
}

// VerificarWebhook verifica la firma X-Sifende-Signature y devuelve el evento. Todo rechazo envuelve
// ErrFirmaWebhookInvalida: la firma no coincide, faltan headers o el timestamp está fuera de la tolerancia.
func VerificarWebhook(opciones OpcionesVerificacion) (*EventoWebhook, error) {
	h := opciones.Headers
	eventID := h.Get("X-Sifende-Event-Id")
	deliveryID := h.Get("X-Sifende-Delivery-Id")
	timestamp := h.Get("X-Sifende-Timestamp")
	firma := h.Get("X-Sifende-Signature")
	if eventID == "" || deliveryID == "" || timestamp == "" || firma == "" {
		return nil, fmt.Errorf("%w: faltan headers X-Sifende-* en el webhook", ErrFirmaWebhookInvalida)
	}

	tolerancia := opciones.Tolerancia
	if tolerancia == 0 {
		tolerancia = ToleranciaWebhookPorDefecto
	}
	ahora := opciones.Ahora
	if ahora.IsZero() {
		ahora = time.Now()
	}
	segundos, err := strconv.ParseInt(timestamp, 10, 64)
	diferencia := math.Abs(float64(ahora.UnixNano())/1e9 - float64(segundos))
	if err != nil || diferencia > tolerancia.Seconds() {
		return nil, fmt.Errorf("%w: X-Sifende-Timestamp está fuera de la tolerancia", ErrFirmaWebhookInvalida)
	}

	var recibidas [][]byte
	for _, parte := range strings.FieldsFunc(firma, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		if valor, ok := strings.CutPrefix(parte, "v1="); ok {
			if bytes, err := base64.StdEncoding.DecodeString(valor); err == nil {
				recibidas = append(recibidas, bytes)
			}
		}
	}
	for _, secreto := range opciones.Secretos {
		esperada := firmar(secreto, eventID, deliveryID, timestamp, opciones.Cuerpo)
		for _, recibida := range recibidas {
			if hmac.Equal(recibida, esperada) {
				var evento EventoWebhook
				if err := json.Unmarshal(opciones.Cuerpo, &evento); err != nil {
					return nil, fmt.Errorf("sifende: el webhook firmado no es un evento: %w", err)
				}
				return &evento, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: la firma no coincide", ErrFirmaWebhookInvalida)
}

// FirmarWebhook calcula X-Sifende-Signature para un cuerpo dado. Sirve para probar tu endpoint.
func FirmarWebhook(secreto, eventID, deliveryID string, timestamp int64, cuerpo []byte) string {
	firma := firmar(secreto, eventID, deliveryID, strconv.FormatInt(timestamp, 10), cuerpo)
	return "v1=" + base64.StdEncoding.EncodeToString(firma)
}

func firmar(secreto, eventID, deliveryID, timestamp string, cuerpo []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secreto))
	mac.Write([]byte(eventID + "." + deliveryID + "." + timestamp + "."))
	mac.Write(cuerpo)
	return mac.Sum(nil)
}
