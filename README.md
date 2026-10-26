# sifende-go

SDK de Sifende para Go: emití facturas electrónicas, notas de crédito y eventos de SIFEN, verificá webhooks y calculá
totales con las mismas reglas que la API.

- Sin dependencias: sólo la biblioteca estándar.
- Tipos generados desde el OpenAPI de la API, con los montos como `Decimal` exacto: nunca pasan por un `float`.
- Reintentos seguros: cada operación tributaria viaja con `Idempotency-Key`.

## Instalación

```bash
go get github.com/ithdev/sifende-go
```

Requiere Go 1.26 o posterior.

Necesitás una API key de Sifende. El prefijo de la clave fija el ambiente: `sk_live_`, `sk_test_` o `sk_sandbox_`. La
referencia completa de la API está en [sifende.com.py/docs](https://sifende.com.py/docs).

> La API key emite documentos a nombre del contribuyente: usá el SDK sólo en el servidor, nunca dentro de una app
> distribuida.

## Uso

```go
cliente, err := sifende.NuevoCliente(os.Getenv("SIFENDE_API_KEY"))
if err != nil {
	return err
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
	return err
}
factura.Pagos = []sifende.PagoFacturaDTO{
	{TipoPago: sifende.TipoPagoTransferencia, MontoPago: totales.TotalGeneral, MonedaPago: sifende.MonedaPYG},
}

documento, err := cliente.Documentos.Emitir(ctx, factura, "pedido-"+pedidoID)
if err != nil {
	return err
}
fmt.Println(documento.CDC, documento.Estado) // PENDIENTE
```

El documento queda en `PENDIENTE` hasta que SIFEN lo procesa. El resultado llega por [webhook](#webhooks) o con
`Documentos.ConsultarEstado(ctx, cdc)`.

En una operación al contado, `MontoPago` tiene que coincidir con el total redondeado. `Totales` (y `CalcularTotales`
para armar el documento a mano) aplica el redondeo oficial de la SET: en guaraníes, al múltiplo de 50 inferior.

`Emitir` acepta cualquier `SolicitudEmision`: `*FacturaElectronicaRequest`, `*NotaCreditoElectronicaRequest`,
`*NotaDebitoElectronicaRequest`, `*NotaRemisionElectronicaRequest` o `*AutofacturaElectronicaRequest`. Cada una envía
su `tipoDocumento` sola.

### Campos opcionales y montos

En una solicitud, un campo opcional que admite cero es un puntero: `nil` no se envía. Con Go 1.26, `new(int32(10))` o
`new(sifende.MustDecimal("12.5"))` lo arma en una línea. Los textos y las enumeraciones opcionales no son punteros: el
valor vacío no se envía.

Los montos, cantidades y tasas son `sifende.Decimal`. Crealos con `NuevoDecimal(150000)`, `ParsearDecimal("19.99")`
o `MustDecimal("19.99")` para literales. `Comparar` los compara sin importar la escala: `1.50` y `1.5` son iguales.

En una respuesta, los campos son valores, y los objetos anidados son punteros que quedan en `nil` si no vinieron.

## Configuración

```go
cliente, err := sifende.NuevoCliente(apiKey,
	sifende.ConTimeout(10*time.Second),
	sifende.ConMaxReintentos(3),
)
```

| Opción | Por defecto | |
|---|---|---|
| `ConTimeout(d)` | 30 s | Tiempo máximo por intento, lectura del cuerpo incluida. |
| `ConMaxReintentos(n)` | 2 | Reintentos ante fallas transitorias. `0` los desactiva. |
| `ConBaseURL(url)` | `https://api.sifende.com.py` | |
| `ConHTTPClient(c)` | `&http.Client{}` | Para usar un proxy o un transporte instrumentado. |

Todas las operaciones reciben un `context.Context`: cancelarlo corta la operación y sus reintentos.

## Operaciones

| Método | Endpoint |
|---|---|
| `Documentos.Emitir(ctx, documento, idempotencyKey)` | `POST /api/v1/documento-electronico` |
| `Documentos.ConsultarEstado(ctx, cdc)` | `GET /api/v1/documento-electronico/status/{cdc}` |
| `Documentos.DescargarKude(ctx, cdc, espera)` | `GET /api/v1/documento-electronico/{cdc}/kude` |
| `Documentos.Cancelar(ctx, cdc, cancelacion, idempotencyKey)` | `POST /api/v1/documento-electronico/{cdc}/cancelar` |
| `Documentos.Nominar(ctx, cdc, nominacion, idempotencyKey)` | `POST /api/v1/documento-electronico/{cdc}/nominar` |
| `Documentos.Inutilizar(ctx, rango, idempotencyKey)` | `POST /api/v1/documento-electronico/inutilizar` |
| `Documentos.ListarEventos(ctx, filtro)` | `GET /api/v1/documento-electronico/eventos` |
| `Documentos.ConsultarEvento(ctx, id)` | `GET /api/v1/documento-electronico/eventos/{id}` |
| `Contribuyente.Consultar(ctx)` | `GET /api/v1/contribuyente` |
| `Contribuyente.ConsultarConsumoPlan(ctx)` | `GET /api/v1/contribuyente/plan-consumo` |
| `Padron.Consultar(ctx, rucOCedula)` | `GET /api/v1/padron/{documento}` |
| `Catalogos.Enumeraciones(ctx)` | `GET /api/v1/public/enums` |
| `Catalogos.Departamentos(ctx)`, `Distritos(ctx, id)`, `Ciudades(ctx, id)` | `GET /api/v1/geografia/…` |

Los catálogos son públicos y no envían la API key.

`DescargarKude` espera hasta 3 minutos a que el PDF esté listo; si no lo está, devuelve `*ErrorKudePendiente`. Con
`espera` distinta de cero cambiás ese plazo.

Para poder retomar una operación después de un corte, generá vos la `idempotencyKey` y guardala antes de enviar. Si
pasás `""`, el SDK genera un UUID que sólo reusa en sus propios reintentos.

## Errores

```go
_, err := cliente.Documentos.Emitir(ctx, factura, idempotencyKey)

var errorAPI *sifende.ErrorAPI
var errorConexion *sifende.ErrorConexion
switch {
case errors.As(err, &errorAPI):
	errorAPI.Status    // 400
	errorAPI.Codigo    // "validation-error", "document-quota-exceeded", …
	errorAPI.Errores() // map[receptor.digitoVerificador:…]
case errors.As(err, &errorConexion):
	errorConexion.IdempotencyKey // repetí la operación con esta clave
	errorConexion.Timeout        // true si venció el timeout, incluso a mitad de la respuesta
}
```

Si cancelás el `context`, el error es el del contexto (`context.Canceled` o la causa que pasaste a
`context.WithCancelCause`), no un error del SDK.

El SDK reintenta sólo los casos que la API marca como seguros, y respeta `Retry-After`:

- Cortes de conexión en consultas y en operaciones con clave de idempotencia.
- `409 idempotency-in-progress`, `503 idempotency-upstream-unknown` y `503 evento-nominacion-error`.
- `429`.
- `500`, `502`, `503` y `504` en consultas.

## Webhooks

La firma se calcula sobre el cuerpo tal como llegó. Pasá los bytes crudos, antes de decodificar el JSON:

```go
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
		if datos, err := evento.Documento(); err == nil {
			log.Println(datos.CDC, datos.Estado)
		}
	}
	w.WriteHeader(http.StatusNoContent)
})
```

Los tipos de evento son `documento.aprobado`, `documento.rechazado`, `documento.cancelado` y `lote.procesado`. Su
`Data` se lee con `evento.Documento()`, `evento.Cancelacion()` o `evento.Lote()`.

Una entrega puede repetirse: deduplicá por `evento.ID`. `VerificarWebhook` rechaza una entrega si su
`X-Sifende-Timestamp` difiere más de 5 minutos de tu reloj; ajustalo con `Tolerancia`. Durante una rotación de secreto,
pasá `Secretos: []string{nuevo, anterior}`. `FirmarWebhook` calcula la firma para probar tu endpoint.

## Desarrollo

```bash
go vet ./... && go test -race ./...
```

`tipos_gen.go` se genera desde el OpenAPI de la API con `internal/generar`, que sólo usa la biblioteca estándar. Para
regenerarlo contra producción o contra una copia local del spec:

```bash
go generate ./...
SIFENDE_OPENAPI=./openapi.json go generate ./...
```

## Licencia

[Apache 2.0](LICENSE). Copyright 2026 ITH E.A.S.; ver [NOTICE](NOTICE).
