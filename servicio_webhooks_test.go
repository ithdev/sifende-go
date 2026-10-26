package sifende

import (
	"net/http"
	"strings"
	"testing"
)

func TestCrearWebhookSinIdempotencyKey(t *testing.T) {
	s := servidor(t, responderJSON(201, map[string]any{
		"endpoint": map[string]any{"id": 7, "url": "https://tienda.com.py/webhooks/sifende", "activo": true},
		"secreto":  "whsec_nuevo",
	}))
	c := nuevo(t, s)

	r, err := c.Webhooks.Crear(t.Context(), CrearWebhookIntegracionRequest{
		URL:     "https://tienda.com.py/webhooks/sifende",
		Eventos: []TipoEventoWebhook{TipoEventoWebhookDocumentoAprobado, TipoEventoWebhookDocumentoRechazado},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.Secreto != "whsec_nuevo" || r.Endpoint == nil || r.Endpoint.ID != 7 {
		t.Errorf("respuesta = %+v", r)
	}
	l := s.registradas()[0]
	if l.metodo != "POST" || l.ruta != "/api/v1/contribuyente/webhooks" {
		t.Errorf("%s %s", l.metodo, l.ruta)
	}
	if l.headers.Get("Idempotency-Key") != "" {
		t.Error("crear un webhook no admite Idempotency-Key")
	}
	if l.cuerpo != `{"url":"https://tienda.com.py/webhooks/sifende","eventos":["documento.aprobado","documento.rechazado"]}` {
		t.Errorf("cuerpo = %s", l.cuerpo)
	}
}

func TestCrearWebhookNoSeRepiteTrasUnCorte(t *testing.T) {
	s := servidor(t, cortar, responderJSON(201, map[string]any{"secreto": "whsec_duplicado"}))
	c := nuevo(t, s)

	_, err := c.Webhooks.Crear(t.Context(), CrearWebhookIntegracionRequest{URL: "https://tienda.com.py/w"})

	como[*ErrorConexion](t, err)
	if n := len(s.registradas()); n != 1 {
		t.Errorf("%d llamadas: un corte no debería repetir la creación", n)
	}
}

func TestRotarSecretoNoSeRepiteTrasUnCorte(t *testing.T) {
	s := servidor(t, cortar, responderJSON(200, map[string]any{"secreto": "whsec_otro"}))
	c := nuevo(t, s)

	_, err := c.Webhooks.RotarSecreto(t.Context(), 7)

	como[*ErrorConexion](t, err)
	llamadas := s.registradas()
	if len(llamadas) != 1 || llamadas[0].ruta != "/api/v1/contribuyente/webhooks/7/rotar" || llamadas[0].headers.Get("Idempotency-Key") != "" {
		t.Errorf("llamadas = %+v", llamadas)
	}
}

func TestListarYConsultarWebhooks(t *testing.T) {
	s := servidor(t,
		responderJSON(200, []any{map[string]any{"id": 7, "eventos": []string{"lote.procesado"}, "ambiente": "DEV"}}),
		problema(404, "webhook-not-found", nil),
	)
	c := nuevo(t, s)

	lista, err := c.Webhooks.Listar(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lista) != 1 || lista[0].ID != 7 || lista[0].Ambiente != AmbienteDev || lista[0].Eventos[0] != "lote.procesado" {
		t.Errorf("lista = %+v", lista)
	}

	_, err = c.Webhooks.Consultar(t.Context(), 8)
	if e := como[*ErrorAPI](t, err); e.Status != 404 || e.Codigo != "webhook-not-found" {
		t.Errorf("error = %+v", e)
	}
	if got := s.registradas()[1].ruta; got != "/api/v1/contribuyente/webhooks/8" {
		t.Errorf("ruta = %q", got)
	}
}

func TestActualizarWebhookEnviaActivoFalse(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"id": 7, "activo": false}))
	c := nuevo(t, s)

	if _, err := c.Webhooks.Actualizar(t.Context(), 7, ActualizarWebhookIntegracionRequest{
		URL:     "https://tienda.com.py/w",
		Eventos: []TipoEventoWebhook{TipoEventoWebhookLoteProcesado},
	}); err != nil {
		t.Fatal(err)
	}

	l := s.registradas()[0]
	if l.metodo != "PUT" || !strings.Contains(l.cuerpo, `"activo":false`) {
		t.Errorf("%s %s", l.metodo, l.cuerpo)
	}
}

func TestEliminarWebhook(t *testing.T) {
	s := servidor(t, func(w http.ResponseWriter, _ *http.Request, _ int) { w.WriteHeader(http.StatusNoContent) })
	c := nuevo(t, s)

	if err := c.Webhooks.Eliminar(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	if l := s.registradas()[0]; l.metodo != "DELETE" || l.ruta != "/api/v1/contribuyente/webhooks/7" {
		t.Errorf("%s %s", l.metodo, l.ruta)
	}
}

func TestListarEntregasConFiltros(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{
		"content": []any{map[string]any{"id": "e1", "estado": "FALLIDA", "cantidadIntentos": 5, "ultimoHttpStatus": 500}},
		"page":    2,
	}))
	c := nuevo(t, s)

	pagina, err := c.Webhooks.ListarEntregas(t.Context(), 7, FiltroEntregas{
		Page: 2, Size: 50, Estado: EstadoEntregaWebhookFallida, TipoEvento: TipoEventoWebhookDocumentoAprobado,
	})
	if err != nil {
		t.Fatal(err)
	}

	if pagina.Content[0].Estado != EstadoEntregaWebhookFallida || pagina.Content[0].UltimoHTTPStatus != 500 {
		t.Errorf("página = %+v", pagina)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/contribuyente/webhooks/7/entregas" || l.query != "estado=FALLIDA&page=2&size=50&tipoEvento=documento.aprobado" {
		t.Errorf("%s?%s", l.ruta, l.query)
	}
}
