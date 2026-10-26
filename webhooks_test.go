package sifende

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	secreto    = "whsec_prueba"
	eventID    = "d44f9f47-380f-4f51-b5c9-4fa335711e18"
	deliveryID = "7b0e3c52-2f7e-4a8e-9a55-1f0f3c4d5e6a"
	timestamp  = int64(1_786_633_200)
)

var (
	ahoraWebhook  = time.Unix(timestamp, 0)
	cuerpoWebhook = `{"id":"` + eventID + `","tipo":"documento.aprobado","version":"1","ocurridoEn":"2026-08-13T15:00:00Z",` +
		`"contribuyenteId":42,"ambiente":"PROD","data":{"documentoId":"x","cdc":"0180","estado":"APROBADO","loteId":815,` +
		`"protocoloAutorizacion":"1","resultados":[]}}`
	// Misma receta que la documentación (y que WebhookSigner en el worker), calculada aparte del SDK.
	firmaEsperada = func() string {
		mac := hmac.New(sha256.New, []byte(secreto))
		mac.Write([]byte(eventID + "." + deliveryID + ".1786633200." + cuerpoWebhook))
		return "v1=" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}()
)

func headersWebhook(firma string) http.Header {
	h := http.Header{}
	h.Set("X-Sifende-Event-Id", eventID)
	h.Set("X-Sifende-Delivery-Id", deliveryID)
	h.Set("X-Sifende-Timestamp", "1786633200")
	h.Set("X-Sifende-Signature", firma)
	return h
}

func verificar(cuerpo string, h http.Header, secretos []string, ahora time.Time) (*EventoWebhook, error) {
	return VerificarWebhook(OpcionesVerificacion{Cuerpo: []byte(cuerpo), Headers: h, Secretos: secretos, Ahora: ahora})
}

func TestAceptaLaFirmaDelWorker(t *testing.T) {
	evento, err := verificar(cuerpoWebhook, headersWebhook(firmaEsperada), []string{secreto}, ahoraWebhook)
	if err != nil {
		t.Fatal(err)
	}

	if evento.ID != eventID || evento.Tipo != TipoEventoWebhookDocumentoAprobado || evento.Ambiente != AmbienteProd {
		t.Errorf("evento = %+v", evento)
	}
	datos, err := evento.Documento()
	if err != nil {
		t.Fatal(err)
	}
	if datos.CDC != "0180" || datos.LoteID == nil || *datos.LoteID != 815 {
		t.Errorf("datos = %+v", datos)
	}
	if _, err := evento.Lote(); err == nil {
		t.Error("un documento.aprobado no trae datos de lote")
	}
}

func TestAceptaVariasFirmasEnElHeader(t *testing.T) {
	h := headersWebhook("v1=b3RyYQ==, " + firmaEsperada)

	if _, err := verificar(cuerpoWebhook, h, []string{secreto}, ahoraWebhook); err != nil {
		t.Fatal(err)
	}
}

func TestAceptaElSecretoAnteriorDuranteUnaRotacion(t *testing.T) {
	if _, err := verificar(cuerpoWebhook, headersWebhook(firmaEsperada), []string{"whsec_nuevo", secreto}, ahoraWebhook); err != nil {
		t.Fatal(err)
	}
}

func TestRechazaWebhooksInvalidos(t *testing.T) {
	casos := map[string]struct {
		cuerpo   string
		headers  http.Header
		secretos []string
		ahora    time.Time
		mensaje  string
	}{
		"cuerpo modificado":   {strings.Replace(cuerpoWebhook, "APROBADO", "RECHAZADO", 1), headersWebhook(firmaEsperada), []string{secreto}, ahoraWebhook, "no coincide"},
		"otro secreto":        {cuerpoWebhook, headersWebhook(firmaEsperada), []string{"whsec_otro"}, ahoraWebhook, "no coincide"},
		"sin secretos":        {cuerpoWebhook, headersWebhook(firmaEsperada), nil, ahoraWebhook, "no coincide"},
		"fuera de tolerancia": {cuerpoWebhook, headersWebhook(firmaEsperada), []string{secreto}, ahoraWebhook.Add(301 * time.Second), "tolerancia"},
		"headers faltantes":   {cuerpoWebhook, http.Header{"X-Sifende-Signature": {firmaEsperada}}, []string{secreto}, ahoraWebhook, "faltan"},
	}
	for nombre, caso := range casos {
		_, err := verificar(caso.cuerpo, caso.headers, caso.secretos, caso.ahora)
		if !errors.Is(err, ErrFirmaWebhookInvalida) || !strings.Contains(err.Error(), caso.mensaje) {
			t.Errorf("%s: error = %v", nombre, err)
		}
	}
}

func TestToleranciaConfigurable(t *testing.T) {
	_, err := VerificarWebhook(OpcionesVerificacion{
		Cuerpo:     []byte(cuerpoWebhook),
		Headers:    headersWebhook(firmaEsperada),
		Secretos:   []string{secreto},
		Ahora:      ahoraWebhook.Add(time.Hour),
		Tolerancia: 2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirmarWebhookProduceLaFirmaDelWorker(t *testing.T) {
	if got := FirmarWebhook(secreto, eventID, deliveryID, timestamp, []byte(cuerpoWebhook)); got != firmaEsperada {
		t.Errorf("firma = %q, se esperaba %q", got, firmaEsperada)
	}
}
