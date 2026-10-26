package sifende

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Version es la versión del SDK. Viaja en el User-Agent.
const Version = "0.2.0"

// URLBase es la URL de la API. Es la misma para los tres ambientes: el ambiente lo fija la API key.
const URLBase = "https://api.sifende.com.py"

var prefijos = []struct {
	prefijo  string
	ambiente Ambiente
}{{"sk_live_", AmbienteProd}, {"sk_test_", AmbienteDev}, {"sk_sandbox_", AmbienteSandbox}}

// Códigos que la guía de idempotencia indica repetir con la misma clave.
var codigosReintentables = []string{"idempotency-in-progress", "idempotency-upstream-unknown"}

// Cliente habla con la API de Sifende. Es seguro usarlo desde varias goroutines.
type Cliente struct {
	Documentos    *ServicioDocumentos
	Contribuyente *ServicioContribuyente
	Padron        *ServicioPadron
	Catalogos     *ServicioCatalogos
	Webhooks      *ServicioWebhooks

	apiKey        string
	ambiente      Ambiente
	baseURL       string
	http          *http.Client
	timeout       time.Duration
	maxReintentos int
	// esperar se reemplaza en los tests para no dormir.
	esperar func(ctx context.Context, d time.Duration) error
}

// Opcion configura el cliente en NuevoCliente.
type Opcion func(*Cliente) error

// ConBaseURL apunta el cliente a otra URL, por ejemplo un servidor de pruebas.
func ConBaseURL(base string) Opcion {
	return func(c *Cliente) error {
		base = strings.TrimRight(base, "/")
		u, err := url.Parse(base)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("sifende: la URL base no es válida: %q", base)
		}
		c.baseURL = base
		return nil
	}
}

// ConHTTPClient usa otro *http.Client, por ejemplo con un proxy o un transporte instrumentado. Su Timeout se suma al
// del SDK: dejalo en cero y usá ConTimeout.
func ConHTTPClient(h *http.Client) Opcion {
	return func(c *Cliente) error {
		if h == nil {
			return errors.New("sifende: el *http.Client es nil")
		}
		c.http = h
		return nil
	}
}

// ConTimeout fija el tiempo máximo de cada intento, lectura del cuerpo incluida. Por defecto 30 s.
func ConTimeout(d time.Duration) Opcion {
	return func(c *Cliente) error {
		if d <= 0 {
			return fmt.Errorf("sifende: el timeout debe ser positivo: %v", d)
		}
		c.timeout = d
		return nil
	}
}

// ConMaxReintentos fija cuántas veces se repite una operación ante una falla transitoria. Por defecto 2; 0 los
// desactiva.
func ConMaxReintentos(n int) Opcion {
	return func(c *Cliente) error {
		if n < 0 {
			return fmt.Errorf("sifende: los reintentos deben ser 0 o más: %d", n)
		}
		c.maxReintentos = n
		return nil
	}
}

// NuevoCliente crea un cliente con la API key del contribuyente: sk_live_…, sk_test_… o sk_sandbox_….
//
// La API key emite documentos a nombre del contribuyente: usala sólo en el servidor.
func NuevoCliente(apiKey string, opciones ...Opcion) (*Cliente, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("sifende: falta la API key")
	}
	c := &Cliente{
		apiKey:        apiKey,
		baseURL:       URLBase,
		http:          &http.Client{},
		timeout:       30 * time.Second,
		maxReintentos: 2,
		esperar:       dormir,
	}
	for _, p := range prefijos {
		if strings.HasPrefix(apiKey, p.prefijo) {
			c.ambiente = p.ambiente
		}
	}
	for _, opcion := range opciones {
		if err := opcion(c); err != nil {
			return nil, err
		}
	}
	c.Documentos = &ServicioDocumentos{c}
	c.Contribuyente = &ServicioContribuyente{c}
	c.Padron = &ServicioPadron{c}
	c.Catalogos = &ServicioCatalogos{c}
	c.Webhooks = &ServicioWebhooks{c}
	return c, nil
}

// Ambiente es el ambiente que fija el prefijo de la API key: sk_live_ → PROD, sk_test_ → DEV, sk_sandbox_ →
// SANDBOX. Vacío para una clave sin prefijo conocido.
func (c *Cliente) Ambiente() Ambiente { return c.ambiente }

type solicitud struct {
	metodo string
	ruta   string
	query  url.Values
	cuerpo any
	// clave es la Idempotency-Key. Con clave, un corte de conexión se reintenta aunque el método no sea GET.
	clave   string
	sinAuth bool
	// acepta es el Accept de una descarga (PDF, XML, PNG): su éxito se devuelve en bytes, sin leerlo como JSON.
	// Vacío es una respuesta JSON.
	acepta string
}

// Accept de cada descarga. Incluyen JSON porque los errores, y el 202 del KuDE, vienen en JSON.
const (
	aceptaPDF = "application/pdf, application/json, application/problem+json"
	aceptaXML = "application/xml, application/json, application/problem+json"
	aceptaPNG = "image/png, application/json, application/problem+json"
)

type respuesta struct {
	status  int
	headers http.Header
	cuerpo  []byte
}

// errJSONIlegible marca un éxito con un JSON que no se pudo leer: se trata como una falla de conexión.
var errJSONIlegible = errors.New("la respuesta no es un JSON válido")

func (c *Cliente) solicitar(ctx context.Context, s solicitud) (*respuesta, error) {
	// Se arma una sola vez: todos los intentos envían los mismos bytes bajo la misma Idempotency-Key.
	destino := c.baseURL + s.ruta
	if len(s.query) > 0 {
		destino += "?" + s.query.Encode()
	}
	var cuerpo []byte
	if s.cuerpo != nil {
		var err error
		if cuerpo, err = json.Marshal(s.cuerpo); err != nil {
			return nil, fmt.Errorf("sifende: no se pudo serializar la solicitud: %w", err)
		}
	}

	for intento := 0; ; intento++ {
		r, err := c.intentar(ctx, s, destino, cuerpo)
		if err != nil {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			reintentable := s.metodo == http.MethodGet || s.clave != ""
			if reintentable && intento < c.maxReintentos {
				if err := c.esperar(ctx, demora(intento)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, errorDeConexion(err, s.clave)
		}
		if r.status >= 200 && r.status < 300 {
			return r, nil
		}

		errorAPI := nuevoErrorAPI(r.status, r.cuerpo, r.headers, s.clave)
		if intento < c.maxReintentos && esReintentable(errorAPI, s) {
			espera := demora(intento)
			if errorAPI.ReintentarEn != nil {
				espera = *errorAPI.ReintentarEn
			}
			if err := c.esperar(ctx, espera); err != nil {
				return nil, err
			}
			continue
		}
		return nil, errorAPI
	}
}

// intentar hace un intento completo. El timeout abarca la lectura del cuerpo: un corte o un timeout a mitad de la
// respuesta es una falla de conexión como cualquier otra.
func (c *Cliente) intentar(ctx context.Context, s solicitud, destino string, cuerpo []byte) (*respuesta, error) {
	ctx, cancelar := context.WithTimeout(ctx, c.timeout)
	defer cancelar()

	var lector io.Reader
	if cuerpo != nil {
		lector = bytes.NewReader(cuerpo)
	}
	req, err := http.NewRequestWithContext(ctx, s.metodo, destino, lector)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if s.acepta != "" {
		req.Header.Set("Accept", s.acepta)
	}
	req.Header.Set("User-Agent", "sifende-sdk-go/"+Version)
	if !s.sinAuth {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if s.clave != "" {
		req.Header.Set("Idempotency-Key", s.clave)
	}
	if cuerpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	leido, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	r := &respuesta{status: res.StatusCode, headers: res.Header, cuerpo: leido}
	exito := r.status >= 200 && r.status < 300
	binario := s.acepta != "" && exito && r.status != http.StatusAccepted
	// Un error HTTP conserva el texto para diagnosticarlo; un éxito ilegible no puede pasar por el DTO esperado.
	if exito && !binario && len(leido) > 0 && esJSON(res.Header) && !json.Valid(leido) {
		return nil, errJSONIlegible
	}
	return r, nil
}

var tipoJSON = regexp.MustCompile(`(?i)json`)

func esJSON(h http.Header) bool { return tipoJSON.MatchString(h.Get("Content-Type")) }

func esReintentable(e *ErrorAPI, s solicitud) bool {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return true
	case slices.Contains(codigosReintentables, e.Codigo):
		return true
	case e.Status == http.StatusServiceUnavailable && e.Codigo == "evento-nominacion-error":
		return true
	}
	return s.metodo == http.MethodGet && slices.Contains([]int{500, 502, 503, 504}, e.Status)
}

func errorDeConexion(err error, clave string) *ErrorConexion {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &ErrorConexion{Mensaje: "la API de Sifende no respondió a tiempo", IdempotencyKey: clave, Timeout: true, Err: err}
	case errors.Is(err, errJSONIlegible):
		return &ErrorConexion{Mensaje: "la API de Sifende respondió un JSON que no se pudo leer", IdempotencyKey: clave, Err: err}
	}
	return &ErrorConexion{Mensaje: "no se pudo conectar con la API de Sifende", IdempotencyKey: clave, Err: err}
}

// demora es el backoff exponencial con jitter: la mitad fija y la otra mitad al azar, hasta 8 s.
func demora(intento int) time.Duration {
	base := min(8*time.Second, 500*time.Millisecond<<min(intento, 10))
	return base/2 + rand.N(base/2+1)
}

func dormir(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}

func (c *Cliente) obtenerJSON(ctx context.Context, s solicitud, destino any) error {
	r, err := c.solicitar(ctx, s)
	if err != nil {
		return err
	}
	return decodificar(r.cuerpo, destino)
}

func decodificar(cuerpo []byte, destino any) error {
	if len(cuerpo) == 0 {
		return nil
	}
	if err := json.Unmarshal(cuerpo, destino); err != nil {
		return fmt.Errorf("sifende: la respuesta no tiene la forma esperada: %w", err)
	}
	return nil
}

// claveAleatoria genera un UUID v4 para la Idempotency-Key que el llamador no indicó.
func claveAleatoria() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func segmento(valor string) string { return url.PathEscape(valor) }
