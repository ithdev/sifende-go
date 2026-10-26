package sifende

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const rutaDocumentos = "/api/v1/documento-electronico"

// ServicioDocumentos emite documentos electrónicos y sus eventos.
type ServicioDocumentos struct{ c *Cliente }

// Emitir envía un documento a Sifende. Responde con el CDC en estado PENDIENTE; el resultado de SIFEN llega por
// webhook o con ConsultarEstado.
//
// idempotencyKey identifica la operación: generala vos (por ejemplo "pedido-1001") y guardala antes de enviar, así
// podés repetirla después de un corte sin duplicar el documento. Si va vacía, el SDK genera un UUID que sólo reusa en
// sus propios reintentos.
func (d *ServicioDocumentos) Emitir(ctx context.Context, documento SolicitudEmision, idempotencyKey string) (*DocumentoElectronicoEmisionResponseDTO, error) {
	var r DocumentoElectronicoEmisionResponseDTO
	err := d.c.obtenerJSON(ctx, solicitud{
		metodo: http.MethodPost,
		ruta:   rutaDocumentos,
		cuerpo: documento,
		clave:  claveOAleatoria(idempotencyKey),
	}, &r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (d *ServicioDocumentos) ConsultarEstado(ctx context.Context, cdc string) (*DocumentoElectronicoStatusDTO, error) {
	var r DocumentoElectronicoStatusDTO
	if err := d.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: rutaDocumentos + "/status/" + segmento(cdc)}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// EsperaKudePorDefecto es cuánto espera DescargarKude si no se indica otra espera.
const EsperaKudePorDefecto = 3 * time.Minute

var errPlazoKude = errors.New("venció la espera del KuDE")

// DescargarKude devuelve el PDF del KuDE. Si Sifende todavía lo está preparando, consulta de nuevo hasta que esté
// listo, como mucho durante espera (EsperaKudePorDefecto si es cero). Si no llega a tiempo devuelve
// *ErrorKudePendiente.
func (d *ServicioDocumentos) DescargarKude(ctx context.Context, cdc string, espera time.Duration) ([]byte, error) {
	if espera <= 0 {
		espera = EsperaKudePorDefecto
	}
	// El plazo cubre todo: cada GET, sus reintentos y las esperas entre consultas.
	plazo, cancelar := context.WithTimeoutCause(ctx, espera, errPlazoKude)
	defer cancelar()
	pdf, err := d.esperarKude(plazo, cdc, time.Now().Add(espera))
	if err != nil && ctx.Err() == nil && errors.Is(context.Cause(plazo), errPlazoKude) {
		return nil, &ErrorKudePendiente{CDC: cdc}
	}
	return pdf, err
}

func (d *ServicioDocumentos) esperarKude(ctx context.Context, cdc string, limite time.Time) ([]byte, error) {
	ruta := rutaDocumentos + "/" + segmento(cdc) + "/kude"
	inicio := time.Now()
	reintentoSinConsultaHecho := false
	soloConsulta := false

	for {
		s := solicitud{metodo: http.MethodGet, ruta: ruta, acepta: aceptaPDF}
		if soloConsulta {
			s.query = url.Values{"soloConsulta": {"true"}}
		}
		r, err := d.c.solicitar(ctx, s)
		if err != nil {
			return nil, err
		}
		if r.status != http.StatusAccepted {
			return r.cuerpo, nil
		}

		espera := 5 * time.Second
		if retry := leerRetryAfter(r.headers.Get("Retry-After"), time.Now()); retry != nil {
			espera = *retry
		}
		if time.Now().Add(espera).After(limite) {
			return nil, &ErrorKudePendiente{CDC: cdc}
		}
		if err := d.c.esperar(ctx, espera); err != nil {
			return nil, err
		}

		// Con soloConsulta no se vuelve a pedir la preparación: a los 2 minutos se repite una vez sin él.
		soloConsulta = reintentoSinConsultaHecho || time.Since(inicio) < 2*time.Minute
		if !soloConsulta {
			reintentoSinConsultaHecho = true
		}
	}
}

// DescargarXML devuelve el XML firmado tal como se envió a SIFEN, con el código QR incluido. Está disponible desde
// que el documento se registra, sin esperar la respuesta de SIFEN; si todavía no tiene XML, la API responde 409
// documento-xml-not-available.
func (d *ServicioDocumentos) DescargarXML(ctx context.Context, cdc string) ([]byte, error) {
	return d.descargar(ctx, "/"+segmento(cdc)+"/xml", nil, aceptaXML)
}

// OpcionesTicket configura DescargarKudeTicket.
type OpcionesTicket struct {
	// Ancho es el ancho del papel en mm: 80 o 58. Cero es 80.
	Ancho int32
	// Resumen omite el detalle de ítems y de impuestos (Manual Técnico V150, cap. 13.7).
	Resumen bool
}

// DescargarKudeTicket devuelve el KuDE en cinta de papel, en PDF, para impresoras térmicas de 80 o 58 mm. Se genera
// en cada pedido. Sólo existe para facturas y notas de crédito o débito: otros tipos responden 501
// kude-not-supported.
func (d *ServicioDocumentos) DescargarKudeTicket(ctx context.Context, cdc string, opciones OpcionesTicket) ([]byte, error) {
	return d.descargar(ctx, "/"+segmento(cdc)+"/kude/ticket", queryTicket("ancho", opciones.Ancho, opciones.Resumen), aceptaPDF)
}

// OpcionesTicketImagen configura DescargarKudeTicketImagen.
type OpcionesTicketImagen struct {
	// Puntos son los puntos por línea de la impresora a 203 dpi: 576 o 512 para papel de 80 mm, 384 para 58 mm. Cero
	// es 576.
	Puntos int32
	// Resumen omite el detalle de ítems y de impuestos (Manual Técnico V150, cap. 13.7).
	Resumen bool
}

// DescargarKudeTicketImagen devuelve el ticket como PNG de 1 bit con el ancho exacto de una línea de la impresora,
// para imprimirlo con el comando de imagen ráster de ESC/POS (GS v 0).
func (d *ServicioDocumentos) DescargarKudeTicketImagen(ctx context.Context, cdc string, opciones OpcionesTicketImagen) ([]byte, error) {
	return d.descargar(ctx, "/"+segmento(cdc)+"/kude/ticket/imagen", queryTicket("puntos", opciones.Puntos, opciones.Resumen), aceptaPNG)
}

func queryTicket(medida string, valor int32, resumen bool) url.Values {
	query := url.Values{}
	if valor != 0 {
		query.Set(medida, strconv.Itoa(int(valor)))
	}
	if resumen {
		query.Set("resumen", "true")
	}
	return query
}

func (d *ServicioDocumentos) descargar(ctx context.Context, ruta string, query url.Values, acepta string) ([]byte, error) {
	r, err := d.c.solicitar(ctx, solicitud{metodo: http.MethodGet, ruta: rutaDocumentos + ruta, query: query, acepta: acepta})
	if err != nil {
		return nil, err
	}
	return r.cuerpo, nil
}

// Cancelar cancela en SIFEN un documento aprobado.
func (d *ServicioDocumentos) Cancelar(ctx context.Context, cdc string, cancelacion CancelacionRequest, idempotencyKey string) (*EventoSifenDTO, error) {
	return d.evento(ctx, rutaDocumentos+"/"+segmento(cdc)+"/cancelar", cancelacion, idempotencyKey)
}

// Nominar identifica al receptor de una factura emitida como innominada.
func (d *ServicioDocumentos) Nominar(ctx context.Context, cdc string, nominacion EventoNominacionRequest, idempotencyKey string) (*EventoSifenDTO, error) {
	return d.evento(ctx, rutaDocumentos+"/"+segmento(cdc)+"/nominar", nominacion, idempotencyKey)
}

// Inutilizar inutiliza en SIFEN un rango de números que no se va a usar.
func (d *ServicioDocumentos) Inutilizar(ctx context.Context, rango EventoInutilizacionRequest, idempotencyKey string) (*EventoSifenDTO, error) {
	return d.evento(ctx, rutaDocumentos+"/inutilizar", rango, idempotencyKey)
}

func (d *ServicioDocumentos) evento(ctx context.Context, ruta string, cuerpo any, idempotencyKey string) (*EventoSifenDTO, error) {
	var r EventoSifenDTO
	err := d.c.obtenerJSON(ctx, solicitud{
		metodo: http.MethodPost,
		ruta:   ruta,
		cuerpo: cuerpo,
		clave:  claveOAleatoria(idempotencyKey),
	}, &r)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FiltroEventos filtra ListarEventos. Los campos en cero no se envían.
type FiltroEventos struct {
	// Page empieza en 0.
	Page int32
	// Size es la cantidad por página. Por defecto 10.
	Size int32
	// TipoEvento es CANCELACION, INUTILIZACION, NOMINACION…
	TipoEvento string
}

func (d *ServicioDocumentos) ListarEventos(ctx context.Context, filtro FiltroEventos) (*PageDTOEventoSifenDTO, error) {
	query := url.Values{}
	if filtro.Page != 0 {
		query.Set("page", strconv.Itoa(int(filtro.Page)))
	}
	if filtro.Size != 0 {
		query.Set("size", strconv.Itoa(int(filtro.Size)))
	}
	if filtro.TipoEvento != "" {
		query.Set("tipoEvento", filtro.TipoEvento)
	}
	var r PageDTOEventoSifenDTO
	if err := d.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: rutaDocumentos + "/eventos", query: query}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (d *ServicioDocumentos) ConsultarEvento(ctx context.Context, eventoID int64) (*EventoSifenDTO, error) {
	var r EventoSifenDTO
	ruta := rutaDocumentos + "/eventos/" + strconv.FormatInt(eventoID, 10)
	if err := d.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: ruta}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func claveOAleatoria(clave string) string {
	if clave == "" {
		return claveAleatoria()
	}
	return clave
}
