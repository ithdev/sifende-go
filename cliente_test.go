package sifende

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type llamada struct {
	metodo  string
	ruta    string
	query   string
	headers http.Header
	cuerpo  string
}

// manejador responde la llamada número n (desde 1). Los manejadores se usan en orden; el último se repite.
type manejador func(w http.ResponseWriter, r *http.Request, n int)

type servidorFalso struct {
	*httptest.Server
	mu       sync.Mutex
	llamadas []llamada
}

func servidor(t *testing.T, manejadores ...manejador) *servidorFalso {
	t.Helper()
	s := &servidorFalso{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.llamadas = append(s.llamadas, llamada{r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Clone(), string(cuerpo)})
		n := len(s.llamadas)
		s.mu.Unlock()
		manejadores[min(n, len(manejadores))-1](w, r, n)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *servidorFalso) registradas() []llamada {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llamada(nil), s.llamadas...)
}

func responderJSON(status int, cuerpo any, headers ...string) manejador {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(cuerpo)
	}
}

func problema(status int, codigo string, extra map[string]any, headers ...string) manejador {
	cuerpo := map[string]any{
		"type":   "https://sifende.com.py/docs/solucion-problemas/" + codigo,
		"title":  "Título",
		"status": status,
		"detail": "Detalle",
	}
	for k, v := range extra {
		cuerpo[k] = v
	}
	return func(w http.ResponseWriter, r *http.Request, n int) {
		responderJSON(status, cuerpo, append([]string{"Content-Type", "application/problem+json"}, headers...)...)(w, r, n)
	}
}

// cortar cierra la conexión sin responder.
func cortar(w http.ResponseWriter, _ *http.Request, _ int) {
	conexion, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conexion.Close()
	}
}

// cortarCuerpo manda los headers y parte del cuerpo, y cierra la conexión.
func cortarCuerpo(w http.ResponseWriter, _ *http.Request, _ int) {
	conexion, buffer, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	escribirCrudo(buffer, "HTTP/1.1 202 Accepted\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{\"cdc\":\"01")
	time.Sleep(5 * time.Millisecond)
	conexion.Close()
}

func escribirCrudo(b *bufio.ReadWriter, texto string) {
	_, _ = b.WriteString(texto)
	_ = b.Flush()
}

func nuevo(t *testing.T, s *servidorFalso, opciones ...Opcion) *Cliente {
	t.Helper()
	c, err := NuevoCliente("sk_test_abc", append([]Opcion{ConBaseURL(s.URL)}, opciones...)...)
	if err != nil {
		t.Fatal(err)
	}
	// El backoff real es de cientos de milisegundos; Retry-After se respeta igual hasta 1 s.
	c.esperar = func(ctx context.Context, d time.Duration) error { return dormir(ctx, min(d, time.Second)) }
	return c
}

func factura() *FacturaElectronicaRequest {
	return &FacturaElectronicaRequest{
		TipoEmision:           TipoEmisionNormal,
		NumeroEstablecimiento: 1,
		PuntoExpedicion:       1,
		TipoTransaccion:       TipoTransaccionVentaMercaderia,
		CondicionOperacion:    CondicionOperacionContado,
		Receptor: ReceptorDTO{
			TipoContribuyente: NaturalezaNoContribuyente,
			TipoOperacion:     TipoOperacionB2C,
			TipoDocumento:     TipoDocumentoIdentidadInnominado,
			NumeroDocumento:   "0",
			NombreRazonSocial: "Sin Nombre",
		},
		Items: []ItemDTO{{
			Codigo:               "P1",
			Descripcion:          "Remera",
			Cantidad:             NuevoDecimal(1),
			UnidadMedida:         "UNI",
			PrecioUnitario:       NuevoDecimal(150000),
			AfectacionTributaria: AfectacionTributariaGravado,
			TasaIVA:              new(int32(10)),
		}},
	}
}

func como[T error](t *testing.T, err error) T {
	t.Helper()
	var objetivo T
	if !errors.As(err, &objetivo) {
		t.Fatalf("se esperaba %T, llegó %T: %v", objetivo, err, err)
	}
	return objetivo
}

func TestAmbientePorPrefijo(t *testing.T) {
	for clave, esperado := range map[string]Ambiente{
		"sk_live_abc": AmbienteProd, "sk_test_abc": AmbienteDev, "sk_sandbox_abc": AmbienteSandbox, "legacy": "",
	} {
		c, err := NuevoCliente(clave)
		if err != nil {
			t.Fatal(err)
		}
		if c.Ambiente() != esperado {
			t.Errorf("%s: ambiente %q, se esperaba %q", clave, c.Ambiente(), esperado)
		}
	}
}

func TestConfiguracionInvalida(t *testing.T) {
	casos := map[string][]Opcion{
		"timeout":    {ConTimeout(-1)},
		"reintentos": {ConMaxReintentos(-1)},
		"baseURL":    {ConBaseURL("no es una url")},
		"http":       {ConHTTPClient(nil)},
	}
	for nombre, opciones := range casos {
		if _, err := NuevoCliente("sk_test_abc", opciones...); err == nil {
			t.Errorf("%s: se esperaba un error", nombre)
		}
	}
	if _, err := NuevoCliente(" "); err == nil {
		t.Error("una API key vacía debería fallar")
	}
}

func TestEmitirEnviaAPIKeyCuerpoYClaveGenerada(t *testing.T) {
	s := servidor(t, responderJSON(202, map[string]any{"cdc": "0180", "estado": "PENDIENTE"}))
	c := nuevo(t, s)

	r, err := c.Documentos.Emitir(t.Context(), factura(), "")
	if err != nil {
		t.Fatal(err)
	}

	if r.CDC != "0180" || r.Estado != "PENDIENTE" {
		t.Errorf("respuesta inesperada: %+v", r)
	}
	l := s.registradas()[0]
	if l.metodo != "POST" || l.ruta != "/api/v1/documento-electronico" {
		t.Errorf("%s %s", l.metodo, l.ruta)
	}
	if got := l.headers.Get("Authorization"); got != "Bearer sk_test_abc" {
		t.Errorf("Authorization = %q", got)
	}
	if got := l.headers.Get("User-Agent"); got != "sifende-sdk-go/"+Version {
		t.Errorf("User-Agent = %q", got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(l.headers.Get("Idempotency-Key")) {
		t.Errorf("Idempotency-Key = %q", l.headers.Get("Idempotency-Key"))
	}
	var cuerpo map[string]any
	if err := json.Unmarshal([]byte(l.cuerpo), &cuerpo); err != nil {
		t.Fatal(err)
	}
	if cuerpo["tipoDocumento"] != "FACTURA_ELECTRONICA" {
		t.Errorf("tipoDocumento = %v", cuerpo["tipoDocumento"])
	}
	item := cuerpo["items"].([]any)[0].(map[string]any)
	if item["precioUnitario"] != 150000.0 || item["tasaIVA"] != 10.0 {
		t.Errorf("ítem = %v", item)
	}
	if strings.Contains(l.cuerpo, "descuentoParticular") {
		t.Errorf("un campo opcional sin valor no debería enviarse: %s", l.cuerpo)
	}
}

func TestEmitirRepiteLaClaveAlReintentar(t *testing.T) {
	s := servidor(t,
		cortar,
		problema(409, "idempotency-in-progress", nil, "Retry-After", "0"),
		responderJSON(202, map[string]any{"cdc": "0180"}),
	)
	c := nuevo(t, s, ConMaxReintentos(3))

	if _, err := c.Documentos.Emitir(t.Context(), factura(), "pedido-1001"); err != nil {
		t.Fatal(err)
	}

	llamadas := s.registradas()
	if len(llamadas) != 3 {
		t.Fatalf("%d llamadas, se esperaban 3", len(llamadas))
	}
	for i, l := range llamadas {
		if got := l.headers.Get("Idempotency-Key"); got != "pedido-1001" {
			t.Errorf("llamada %d: Idempotency-Key = %q", i, got)
		}
	}
}

func TestProblemDetailsEnErrorAPI(t *testing.T) {
	s := servidor(t, problema(400, "validation-error", map[string]any{
		"errores": map[string]string{"receptor.digitoVerificador": "Obligatorio"},
		"traceId": "abc123",
	}))
	c := nuevo(t, s)

	_, err := c.Documentos.Emitir(t.Context(), factura(), "k1")

	e := como[*ErrorAPI](t, err)
	if e.Status != 400 || e.Codigo != "validation-error" || e.TraceID != "abc123" || e.IdempotencyKey != "k1" {
		t.Errorf("error inesperado: %+v", e)
	}
	if e.Errores()["receptor.digitoVerificador"] != "Obligatorio" {
		t.Errorf("errores = %v", e.Errores())
	}
	if e.Error() != "sifende: 400 Título: Detalle" {
		t.Errorf("mensaje = %q", e.Error())
	}
}

func TestNoReintentaUn500EnUnaEmision(t *testing.T) {
	s := servidor(t, problema(500, "internal-error", nil))
	c := nuevo(t, s)

	_, err := c.Documentos.Emitir(t.Context(), factura(), "")

	como[*ErrorAPI](t, err)
	if n := len(s.registradas()); n != 1 {
		t.Errorf("%d llamadas, se esperaba 1", n)
	}
}

func TestErrorConexionInformaLaClave(t *testing.T) {
	s := servidor(t, cortar)
	c := nuevo(t, s, ConMaxReintentos(0))

	_, err := c.Documentos.Emitir(t.Context(), factura(), "k2")

	if e := como[*ErrorConexion](t, err); e.IdempotencyKey != "k2" || e.Timeout {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestTimeout(t *testing.T) {
	s := servidor(t, func(_ http.ResponseWriter, r *http.Request, _ int) { <-r.Context().Done() })
	c := nuevo(t, s, ConTimeout(20*time.Millisecond), ConMaxReintentos(0))

	_, err := c.Documentos.Emitir(t.Context(), factura(), "lento")

	if e := como[*ErrorConexion](t, err); !e.Timeout || e.IdempotencyKey != "lento" {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestReintentaUnGETAnteUn503YCodificaElCDC(t *testing.T) {
	s := servidor(t,
		problema(503, "internal-error", nil, "Retry-After", "0"),
		responderJSON(200, map[string]any{"estado": "APROBADO"}),
	)
	c := nuevo(t, s)

	estado, err := c.Documentos.ConsultarEstado(t.Context(), "0180/x")
	if err != nil {
		t.Fatal(err)
	}

	if estado.Estado != "APROBADO" {
		t.Errorf("estado = %q", estado.Estado)
	}
	llamadas := s.registradas()
	if len(llamadas) != 2 || llamadas[0].ruta != "/api/v1/documento-electronico/status/0180%2Fx" {
		t.Errorf("llamadas = %+v", llamadas)
	}
}

func TestReintentaUn500EnUnaConsulta(t *testing.T) {
	s := servidor(t,
		responderJSON(500, map[string]any{"type": "https://sifende.com.py/problemas/internal-error"}, "Retry-After", "0"),
		responderJSON(200, map[string]any{"estado": "APROBADO"}),
	)
	c := nuevo(t, s)

	if _, err := c.Documentos.ConsultarEstado(t.Context(), "0180"); err != nil {
		t.Fatal(err)
	}
	if n := len(s.registradas()); n != 2 {
		t.Errorf("%d llamadas, se esperaban 2", n)
	}
}

func TestPadronDevuelveData(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"data": map[string]any{"nombreRazonSocial": "ACME S.A."}, "timestamp": "x", "errors": []any{}}))
	c := nuevo(t, s)

	p, err := c.Padron.Consultar(t.Context(), "80012345-6")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || p.NombreRazonSocial != "ACME S.A." {
		t.Errorf("padrón = %+v", p)
	}
	if got := s.registradas()[0].ruta; got != "/api/v1/padron/80012345-6" {
		t.Errorf("ruta = %q", got)
	}
}

func TestCatalogosSinAPIKey(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"tipoPago": []any{map[string]any{"name": "EFECTIVO", "val": 1}}}))
	c, err := NuevoCliente("sk_test_abc", ConBaseURL(s.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}

	enums, err := c.Catalogos.Enumeraciones(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if enums["tipoPago"][0].Name != "EFECTIVO" {
		t.Errorf("enums = %+v", enums)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/public/enums" || l.headers.Get("Authorization") != "" {
		t.Errorf("llamada = %+v", l)
	}
}

func TestFiltrosDeEventos(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"content": []any{}, "page": 1}))
	c := nuevo(t, s)

	if _, err := c.Documentos.ListarEventos(t.Context(), FiltroEventos{Page: 1, Size: 20, TipoEvento: "CANCELACION"}); err != nil {
		t.Fatal(err)
	}
	if got := s.registradas()[0].query; got != "page=1&size=20&tipoEvento=CANCELACION" {
		t.Errorf("query = %q", got)
	}
}

func TestCancelarConMotivoEIdempotencyKey(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"estadoEvento": "APROBADO"}))
	c := nuevo(t, s)

	if _, err := c.Documentos.Cancelar(t.Context(), "0180", CancelacionRequest{Motivo: "Pedido cancelado por el cliente"}, ""); err != nil {
		t.Fatal(err)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/documento-electronico/0180/cancelar" || l.headers.Get("Idempotency-Key") == "" {
		t.Errorf("llamada = %+v", l)
	}
	if l.cuerpo != `{"motivo":"Pedido cancelado por el cliente"}` {
		t.Errorf("cuerpo = %s", l.cuerpo)
	}
}

func TestReintentaLaNominacionAnteEventoNominacionError(t *testing.T) {
	s := servidor(t,
		problema(503, "evento-nominacion-error", nil, "Retry-After", "0"),
		responderJSON(200, map[string]any{"estadoEvento": "APROBADO"}),
	)
	c := nuevo(t, s)

	evento, err := c.Documentos.Nominar(t.Context(), "0180", EventoNominacionRequest{
		Motivo: "Identificación del cliente a su solicitud",
		Receptor: ReceptorNominadoRequest{
			Naturaleza:        NaturalezaNoContribuyente,
			TipoOperacion:     TipoOperacionB2C,
			Pais:              PaisPRY,
			TipoDocumento:     TipoDocumentoIdentidadCedulaParaguaya,
			NumeroDocumento:   "1234567",
			NombreRazonSocial: "Juan Pérez",
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	if evento.EstadoEvento != "APROBADO" {
		t.Errorf("evento = %+v", evento)
	}
	llamadas := s.registradas()
	if llamadas[0].headers.Get("Idempotency-Key") != llamadas[1].headers.Get("Idempotency-Key") {
		t.Error("el reintento cambió la Idempotency-Key")
	}
}

func TestReintentaConLaMismaClaveSiSeCortaElCuerpo(t *testing.T) {
	s := servidor(t, cortarCuerpo, responderJSON(202, map[string]any{"cdc": "0180", "estado": "PENDIENTE"}))
	c := nuevo(t, s, ConMaxReintentos(1))

	r, err := c.Documentos.Emitir(t.Context(), factura(), "corte-1")
	if err != nil {
		t.Fatal(err)
	}

	if r.CDC != "0180" {
		t.Errorf("cdc = %q", r.CDC)
	}
	for _, l := range s.registradas() {
		if l.headers.Get("Idempotency-Key") != "corte-1" {
			t.Errorf("Idempotency-Key = %q", l.headers.Get("Idempotency-Key"))
		}
	}
}

func TestCuerpoCortadoEnElUltimoIntento(t *testing.T) {
	s := servidor(t, cortarCuerpo)
	c := nuevo(t, s, ConMaxReintentos(0))

	_, err := c.Documentos.Emitir(t.Context(), factura(), "corte-2")

	if e := como[*ErrorConexion](t, err); e.IdempotencyKey != "corte-2" {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestCuerpoQueNoTerminaDeLlegar(t *testing.T) {
	s := servidor(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"cdc":`))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	c := nuevo(t, s, ConTimeout(50*time.Millisecond), ConMaxReintentos(0))

	_, err := c.Documentos.Emitir(t.Context(), factura(), "lento")

	if e := como[*ErrorConexion](t, err); !e.Timeout || e.IdempotencyKey != "lento" {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestJSONIlegibleNoEsUnExito(t *testing.T) {
	s := servidor(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"cdc":`))
	})
	c := nuevo(t, s, ConMaxReintentos(0))

	r, err := c.Documentos.Emitir(t.Context(), factura(), "roto")

	if r != nil {
		t.Errorf("no debería devolver datos: %+v", r)
	}
	if e := como[*ErrorConexion](t, err); e.IdempotencyKey != "roto" {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestMismoCuerpoEnCadaIntento(t *testing.T) {
	documento := factura()
	s := servidor(t, func(w http.ResponseWriter, r *http.Request, n int) {
		documento.Items[0].PrecioUnitario = NuevoDecimal(999)
		cortar(w, r, n)
	}, responderJSON(202, map[string]any{"cdc": "0180"}))
	c := nuevo(t, s, ConMaxReintentos(1))

	if _, err := c.Documentos.Emitir(t.Context(), documento, "mutable"); err != nil {
		t.Fatal(err)
	}

	llamadas := s.registradas()
	if len(llamadas) != 2 || llamadas[0].cuerpo != llamadas[1].cuerpo {
		t.Errorf("los intentos enviaron cuerpos distintos: %+v", llamadas)
	}
}

func TestProblemDetailsConTypeQueNoEsTexto(t *testing.T) {
	s := servidor(t, responderJSON(400, map[string]any{"type": 17, "title": "Error"}, "Content-Type", "application/problem+json"))
	c := nuevo(t, s)

	_, err := c.Documentos.Emitir(t.Context(), factura(), "tipo")

	e := como[*ErrorAPI](t, err)
	if e.Status != 400 || e.IdempotencyKey != "tipo" || e.Codigo != "" || e.Problema != nil {
		t.Errorf("error inesperado: %+v", e)
	}
}

func TestCancelacionDelLlamador(t *testing.T) {
	s := servidor(t, func(_ http.ResponseWriter, r *http.Request, _ int) { <-r.Context().Done() })
	c := nuevo(t, s)
	causa := errors.New("cancelado")
	ctx, cancelar := context.WithCancelCause(t.Context())
	time.AfterFunc(20*time.Millisecond, func() { cancelar(causa) })

	_, err := c.Documentos.ConsultarEstado(ctx, "0180")

	if !errors.Is(err, causa) {
		t.Errorf("se esperaba la causa de la cancelación, llegó %v", err)
	}
}

func pdf(w http.ResponseWriter, _ *http.Request, _ int) {
	w.Header().Set("Content-Type", "application/pdf")
	_, _ = w.Write([]byte("%PDF"))
}

func TestKudeEsperaYConsultaConSoloConsulta(t *testing.T) {
	s := servidor(t, responderJSON(202, map[string]any{"estado": "PENDIENTE", "url": nil}, "Retry-After", "0"), pdf)
	c := nuevo(t, s)

	contenido, err := c.Documentos.DescargarKude(t.Context(), "0180", 0)
	if err != nil {
		t.Fatal(err)
	}

	if string(contenido) != "%PDF" {
		t.Errorf("contenido = %q", contenido)
	}
	llamadas := s.registradas()
	if llamadas[0].query != "" || llamadas[1].query != "soloConsulta=true" {
		t.Errorf("queries = %q, %q", llamadas[0].query, llamadas[1].query)
	}
	if !strings.Contains(llamadas[0].headers.Get("Accept"), "application/pdf") {
		t.Errorf("Accept = %q", llamadas[0].headers.Get("Accept"))
	}
}

func TestKudePendienteSiNoEstaListo(t *testing.T) {
	s := servidor(t, responderJSON(202, map[string]any{"estado": "PENDIENTE"}, "Retry-After", "5"))
	c := nuevo(t, s)

	_, err := c.Documentos.DescargarKude(t.Context(), "0180", time.Second)

	if e := como[*ErrorKudePendiente](t, err); e.CDC != "0180" {
		t.Errorf("cdc = %q", e.CDC)
	}
}

func TestKudeRespetaLaEsperaDuranteLosReintentos(t *testing.T) {
	s := servidor(t,
		responderJSON(202, map[string]any{"estado": "PENDIENTE"}, "Retry-After", "0"),
		responderJSON(503, map[string]any{"type": "x/kude-unavailable"}, "Retry-After", "0.2"),
		pdf,
	)
	c := nuevo(t, s)

	inicio := time.Now()
	_, err := c.Documentos.DescargarKude(t.Context(), "0180", 50*time.Millisecond)

	como[*ErrorKudePendiente](t, err)
	if d := time.Since(inicio); d > 150*time.Millisecond {
		t.Errorf("tardó %v", d)
	}
	if n := len(s.registradas()); n != 2 {
		t.Errorf("%d llamadas, se esperaban 2", n)
	}
}

func TestKudeRespetaLaEsperaSiElServidorTarda(t *testing.T) {
	s := servidor(t, func(w http.ResponseWriter, r *http.Request, n int) {
		select {
		case <-time.After(300 * time.Millisecond):
			pdf(w, r, n)
		case <-r.Context().Done():
		}
	})
	c := nuevo(t, s)

	inicio := time.Now()
	_, err := c.Documentos.DescargarKude(t.Context(), "0180", 50*time.Millisecond)

	como[*ErrorKudePendiente](t, err)
	if d := time.Since(inicio); d > 200*time.Millisecond {
		t.Errorf("tardó %v", d)
	}
}

func TestKudeDistingueLaCancelacionDelPlazo(t *testing.T) {
	s := servidor(t, func(_ http.ResponseWriter, r *http.Request, _ int) { <-r.Context().Done() })
	c := nuevo(t, s)
	causa := errors.New("cancelado")
	ctx, cancelar := context.WithCancelCause(t.Context())
	time.AfterFunc(20*time.Millisecond, func() { cancelar(causa) })

	_, err := c.Documentos.DescargarKude(ctx, "0180", 5*time.Second)

	var pendiente *ErrorKudePendiente
	if errors.As(err, &pendiente) || !errors.Is(err, causa) {
		t.Errorf("se esperaba la causa de la cancelación, llegó %v", err)
	}
}

func TestSolicitudesDeEmisionLlevanSuTipoDocumento(t *testing.T) {
	casos := []struct {
		solicitud SolicitudEmision
		tipo      TipoDocumento
	}{
		{&FacturaElectronicaRequest{}, TipoDocumentoFacturaElectronica},
		{&NotaCreditoElectronicaRequest{}, TipoDocumentoNotaDeCreditoElectronica},
		{&NotaDebitoElectronicaRequest{}, TipoDocumentoNotaDeDebitoElectronica},
		{&NotaRemisionElectronicaRequest{}, TipoDocumentoNotaDeRemisionElectronica},
		{&AutofacturaElectronicaRequest{}, TipoDocumentoAutofacturaElectronica},
	}
	for _, caso := range casos {
		crudo, err := json.Marshal(caso.solicitud)
		if err != nil {
			t.Fatal(err)
		}
		var cuerpo map[string]any
		_ = json.Unmarshal(crudo, &cuerpo)
		if cuerpo["tipoDocumento"] != string(caso.tipo) || caso.solicitud.TipoDocumento() != caso.tipo {
			t.Errorf("%T: tipoDocumento = %v", caso.solicitud, cuerpo["tipoDocumento"])
		}
	}
}

func TestRetryAfterComoFecha(t *testing.T) {
	ahora := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	espera := leerRetryAfter(ahora.Add(3*time.Second).Format(http.TimeFormat), ahora)
	if espera == nil || *espera != 3*time.Second {
		t.Errorf("espera = %v", espera)
	}
	if leerRetryAfter("mañana", ahora) != nil {
		t.Error("un Retry-After ilegible debería ignorarse")
	}
}

func TestDemora(t *testing.T) {
	for intento, tope := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second} {
		if d := demora(intento); d < tope/2 || d > tope {
			t.Errorf("intento %d: %v fuera de [%v, %v]", intento, d, tope/2, tope)
		}
	}
	if d := demora(50); d > 8*time.Second {
		t.Errorf("la demora no debería pasar de 8 s: %v", d)
	}
}

func ExampleErrorAPI() {
	e := nuevoErrorAPI(429, []byte(`{"type":"https://sifende.com.py/docs/solucion-problemas/rate-limited","title":"Demasiadas solicitudes"}`),
		http.Header{"Retry-After": {"2"}}, "pedido-1001")
	fmt.Println(e.Codigo, *e.ReintentarEn, e)
	// Output: rate-limited 2s sifende: 429 Demasiadas solicitudes
}
