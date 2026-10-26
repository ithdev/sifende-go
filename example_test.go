package sifende_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/ithdev/sifende-go"
)

func Example() {
	ctx := context.Background()
	cliente, err := sifende.NuevoCliente(os.Getenv("SIFENDE_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	pedidoID := "1001" // el id de la venta en tu sistema

	factura := &sifende.FacturaElectronicaRequest{
		TipoEmision:           sifende.TipoEmisionNormal,
		NumeroEstablecimiento: 1,
		PuntoExpedicion:       2,
		TipoTransaccion:       sifende.TipoTransaccionVentaMercaderia,
		IndicadorPresencia:    sifende.IndicadorPresenciaOperacionElectronica,
		CondicionOperacion:    sifende.CondicionOperacionContado,
		Receptor: sifende.ReceptorDTO{
			TipoContribuyente: sifende.NaturalezaNoContribuyente,
			TipoOperacion:     sifende.TipoOperacionB2C,
			TipoDocumento:     sifende.TipoDocumentoIdentidadInnominado,
			NumeroDocumento:   "0",
			NombreRazonSocial: "Sin Nombre",
		},
		Items: []sifende.ItemDTO{
			{
				Codigo: "REM-01", Descripcion: "Remera", Cantidad: sifende.NuevoDecimal(2), UnidadMedida: "UNI",
				PrecioUnitario: sifende.NuevoDecimal(150_000), AfectacionTributaria: sifende.AfectacionTributariaGravado,
				TasaIVA: new(int32(10)),
			},
			{
				Codigo: "ENVIO", Descripcion: "Envío", Cantidad: sifende.NuevoDecimal(1), UnidadMedida: "UNI",
				PrecioUnitario: sifende.NuevoDecimal(25_000), AfectacionTributaria: sifende.AfectacionTributariaGravado,
				TasaIVA: new(int32(10)),
			},
		},
	}
	totales, err := factura.Totales()
	if err != nil {
		log.Fatal(err)
	}
	factura.Pagos = []sifende.PagoFacturaDTO{
		{TipoPago: sifende.TipoPagoTransferencia, MontoPago: totales.TotalGeneral, MonedaPago: sifende.MonedaPYG},
	}

	documento, err := cliente.Documentos.Emitir(ctx, factura, "pedido-"+pedidoID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(documento.CDC, documento.Estado) // PENDIENTE
}

func ExampleServicioDocumentos_Emitir_errores() {
	ctx := context.Background()
	cliente, _ := sifende.NuevoCliente(os.Getenv("SIFENDE_API_KEY"))
	var factura *sifende.FacturaElectronicaRequest

	_, err := cliente.Documentos.Emitir(ctx, factura, "pedido-1001")

	var errorAPI *sifende.ErrorAPI
	var errorConexion *sifende.ErrorConexion
	switch {
	case errors.As(err, &errorAPI):
		fmt.Println(errorAPI.Status, errorAPI.Codigo, errorAPI.Errores())
	case errors.As(err, &errorConexion):
		// La operación pudo haberse ejecutado: repetila con la misma clave.
		fmt.Println("reintentar con", errorConexion.IdempotencyKey)
	}
}

func ExampleVerificarWebhook() {
	http.HandleFunc("POST /webhooks/sifende", func(w http.ResponseWriter, r *http.Request) {
		cuerpo, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		evento, err := sifende.VerificarWebhook(sifende.OpcionesVerificacion{
			Cuerpo:   cuerpo,
			Headers:  r.Header,
			Secretos: []string{os.Getenv("SIFENDE_WEBHOOK_SECRET")},
		})
		if errors.Is(err, sifende.ErrFirmaWebhookInvalida) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if evento.Tipo == sifende.TipoEventoWebhookDocumentoAprobado {
			datos, err := evento.Documento()
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			log.Println(datos.CDC, datos.Estado)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
