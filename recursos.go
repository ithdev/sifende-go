package sifende

import (
	"context"
	"net/http"
	"strconv"
)

// ServicioContribuyente consulta los datos del contribuyente de la API key.
type ServicioContribuyente struct{ c *Cliente }

// Consultar devuelve el RUC, los establecimientos, los puntos de expedición y si el contribuyente está listo para
// emitir.
func (r *ServicioContribuyente) Consultar(ctx context.Context) (*ContribuyenteIntegracionDTO, error) {
	var datos ContribuyenteIntegracionDTO
	if err := r.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: "/api/v1/contribuyente"}, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

func (r *ServicioContribuyente) ConsultarConsumoPlan(ctx context.Context) (*PlanConsumoDTO, error) {
	var datos PlanConsumoDTO
	if err := r.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: "/api/v1/contribuyente/plan-consumo"}, &datos); err != nil {
		return nil, err
	}
	return &datos, nil
}

// ServicioPadron busca contribuyentes y personas en el padrón de la DNIT.
type ServicioPadron struct{ c *Cliente }

// Consultar busca un RUC o una cédula, con o sin puntos y dígito verificador. Los campos encajan con el receptor.
// Devuelve nil si la API no trae datos.
func (r *ServicioPadron) Consultar(ctx context.Context, rucOCedula string) (*Padron, error) {
	var datos ApiResponsePadron
	if err := r.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: "/api/v1/padron/" + segmento(rucOCedula)}, &datos); err != nil {
		return nil, err
	}
	return datos.Data, nil
}

// ServicioCatalogos consulta los catálogos públicos de la API. No envían la API key.
type ServicioCatalogos struct{ c *Cliente }

// Enumeraciones devuelve los valores aceptados por cada enumeración de la API (unidadMedida, tipoPago…).
func (r *ServicioCatalogos) Enumeraciones(ctx context.Context) (map[string][]EnumValueDTO, error) {
	var datos map[string][]EnumValueDTO
	if err := r.obtener(ctx, "/api/v1/public/enums", &datos); err != nil {
		return nil, err
	}
	return datos, nil
}

func (r *ServicioCatalogos) Departamentos(ctx context.Context) ([]DepartamentoDTO, error) {
	var datos ApiResponseListDepartamentoDTO
	if err := r.obtener(ctx, "/api/v1/geografia/departamentos", &datos); err != nil {
		return nil, err
	}
	return datos.Data, nil
}

func (r *ServicioCatalogos) Distritos(ctx context.Context, departamentoID int32) ([]DistritoDTO, error) {
	var datos ApiResponseListDistritoDTO
	ruta := "/api/v1/geografia/departamentos/" + strconv.Itoa(int(departamentoID)) + "/distritos"
	if err := r.obtener(ctx, ruta, &datos); err != nil {
		return nil, err
	}
	return datos.Data, nil
}

func (r *ServicioCatalogos) Ciudades(ctx context.Context, distritoID int32) ([]CiudadDTO, error) {
	var datos ApiResponseListCiudadDTO
	ruta := "/api/v1/geografia/distritos/" + strconv.Itoa(int(distritoID)) + "/ciudades"
	if err := r.obtener(ctx, ruta, &datos); err != nil {
		return nil, err
	}
	return datos.Data, nil
}

func (r *ServicioCatalogos) obtener(ctx context.Context, ruta string, destino any) error {
	return r.c.obtenerJSON(ctx, solicitud{metodo: http.MethodGet, ruta: ruta, sinAuth: true}, destino)
}
