package sifende

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

const rutaWebhooks = "/api/v1/contribuyente/webhooks"

// ServicioWebhooks administra los webhooks del contribuyente en el ambiente de la API key. Para verificar las
// entregas que llegan a tu endpoint, usá VerificarWebhook.
//
// Crear y RotarSecreto no admiten Idempotency-Key, así que el SDK no los repite ante un corte de conexión: el secreto
// sólo viaja en esa respuesta.
type ServicioWebhooks struct{ c *Cliente }

// Listar devuelve los webhooks del ambiente de la API key, los más recientes primero, sin el secreto.
func (w *ServicioWebhooks) Listar(ctx context.Context) ([]WebhookEndpointDTO, error) {
	var datos []WebhookEndpointDTO
	if err := w.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: rutaWebhooks}, &datos); err != nil {
		return nil, err
	}
	return datos, nil
}

// Crear registra una URL HTTPS pública para recibir eventos. Hay hasta cinco webhooks activos por ambiente.
//
// El secreto whsec_… con el que se firman las entregas llega sólo en esta respuesta: guardalo. Si se corta la
// conexión, consultá Listar antes de repetir: una URL ya registrada responde 400 validation-error.
func (w *ServicioWebhooks) Crear(ctx context.Context, webhook CrearWebhookIntegracionRequest) (*WebhookSecretResponse, error) {
	var datos WebhookSecretResponse
	if err := w.c.obtenerJSON(ctx, solicitud{metodo: http.MethodPost, ruta: rutaWebhooks, cuerpo: webhook}, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

// Consultar devuelve un webhook, sin el secreto. Uno de otro ambiente responde 404.
func (w *ServicioWebhooks) Consultar(ctx context.Context, webhookID int64) (*WebhookEndpointDTO, error) {
	var datos WebhookEndpointDTO
	if err := w.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: rutaWebhook(webhookID)}, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

// Actualizar reemplaza la URL, los eventos y el estado. Activo: false pausa el webhook y cancela sus entregas
// pendientes. El secreto no cambia: para eso, RotarSecreto.
func (w *ServicioWebhooks) Actualizar(ctx context.Context, webhookID int64, webhook ActualizarWebhookIntegracionRequest) (*WebhookEndpointDTO, error) {
	var datos WebhookEndpointDTO
	err := w.c.obtenerJSON(ctx, solicitud{metodo: http.MethodPut, ruta: rutaWebhook(webhookID), cuerpo: webhook}, &datos)
	if err != nil {
		return nil, err
	}
	return &datos, nil
}

// Eliminar borra el webhook y cancela sus entregas pendientes. No se puede deshacer.
func (w *ServicioWebhooks) Eliminar(ctx context.Context, webhookID int64) error {
	_, err := w.c.solicitar(ctx, solicitud{metodo: http.MethodDelete, ruta: rutaWebhook(webhookID)})
	return err
}

// RotarSecreto genera un secreto nuevo y deja de firmar con el anterior, también las entregas pendientes. El secreto
// llega sólo en esta respuesta; si no la recibiste, volvé a rotar.
//
// Para no perder entregas durante el cambio, verificá con los dos secretos hasta actualizar tu endpoint:
// OpcionesVerificacion{Secretos: []string{nuevo, anterior}}.
func (w *ServicioWebhooks) RotarSecreto(ctx context.Context, webhookID int64) (*WebhookSecretResponse, error) {
	var datos WebhookSecretResponse
	if err := w.c.obtenerJSON(ctx, solicitud{metodo: http.MethodPost, ruta: rutaWebhook(webhookID) + "/rotar"}, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

// FiltroEntregas filtra ListarEntregas. Los campos en cero no se envían.
type FiltroEntregas struct {
	// Page empieza en 0.
	Page int32
	// Size va de 1 a 100. Por defecto 20.
	Size       int32
	Estado     EstadoEntregaWebhook
	TipoEvento TipoEventoWebhook
}

// ListarEntregas devuelve el historial de entregas del webhook, las más recientes primero: estado, intentos y último
// error. Se conserva 90 días.
func (w *ServicioWebhooks) ListarEntregas(ctx context.Context, webhookID int64, filtro FiltroEntregas) (*WebhookEntregaPaginaDTO, error) {
	query := url.Values{}
	if filtro.Page != 0 {
		query.Set("page", strconv.Itoa(int(filtro.Page)))
	}
	if filtro.Size != 0 {
		query.Set("size", strconv.Itoa(int(filtro.Size)))
	}
	if filtro.Estado != "" {
		query.Set("estado", string(filtro.Estado))
	}
	if filtro.TipoEvento != "" {
		query.Set("tipoEvento", string(filtro.TipoEvento))
	}
	var datos WebhookEntregaPaginaDTO
	s := solicitud{metodo: http.MethodGet, ruta: rutaWebhook(webhookID) + "/entregas", query: query}
	if err := w.c.obtenerJSON(ctx, s, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

func rutaWebhook(id int64) string {
	return rutaWebhooks + "/" + strconv.FormatInt(id, 10)
}
