package sifende

import (
	"net/http"
	"strings"
	"testing"
)

func responderBytes(tipo string, cuerpo []byte) manejador {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", tipo)
		_, _ = w.Write(cuerpo)
	}
}

func TestDescargarXMLDevuelveLosBytesTalCualLlegan(t *testing.T) {
	xml := []byte("\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"UTF-8\"?><rDE><DE Id=\"0180\"/></rDE>")
	s := servidor(t, responderBytes("application/xml", xml))
	c := nuevo(t, s)

	contenido, err := c.Documentos.DescargarXML(t.Context(), "0180")
	if err != nil {
		t.Fatal(err)
	}

	if string(contenido) != string(xml) {
		t.Errorf("contenido = %q", contenido)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/documento-electronico/0180/xml" || !strings.HasPrefix(l.headers.Get("Accept"), "application/xml") {
		t.Errorf("%s Accept=%q", l.ruta, l.headers.Get("Accept"))
	}
}

func TestDescargarXMLSinXMLTodavia(t *testing.T) {
	s := servidor(t, problema(409, "documento-xml-not-available", nil))
	c := nuevo(t, s)

	_, err := c.Documentos.DescargarXML(t.Context(), "0180")

	if e := como[*ErrorAPI](t, err); e.Status != 409 || e.Codigo != "documento-xml-not-available" {
		t.Errorf("error = %+v", e)
	}
}

func TestDescargarKudeTicket(t *testing.T) {
	s := servidor(t, pdf)
	c := nuevo(t, s)

	if _, err := c.Documentos.DescargarKudeTicket(t.Context(), "0180", OpcionesTicket{}); err != nil {
		t.Fatal(err)
	}
	contenido, err := c.Documentos.DescargarKudeTicket(t.Context(), "0180", OpcionesTicket{Ancho: 58, Resumen: true})
	if err != nil {
		t.Fatal(err)
	}

	if string(contenido) != "%PDF" {
		t.Errorf("contenido = %q", contenido)
	}
	llamadas := s.registradas()
	if llamadas[0].ruta != "/api/v1/documento-electronico/0180/kude/ticket" || llamadas[0].query != "" {
		t.Errorf("%s?%s", llamadas[0].ruta, llamadas[0].query)
	}
	if llamadas[1].query != "ancho=58&resumen=true" {
		t.Errorf("query = %q", llamadas[1].query)
	}
}

func TestDescargarKudeTicketDeUnTipoSinTicket(t *testing.T) {
	s := servidor(t, problema(501, "kude-not-supported", nil))
	c := nuevo(t, s)

	_, err := c.Documentos.DescargarKudeTicket(t.Context(), "0180", OpcionesTicket{})

	if e := como[*ErrorAPI](t, err); e.Status != 501 || e.Codigo != "kude-not-supported" {
		t.Errorf("error = %+v", e)
	}
	if n := len(s.registradas()); n != 1 {
		t.Errorf("%d llamadas: un 501 no se reintenta", n)
	}
}

func TestDescargarKudeTicketImagen(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	s := servidor(t, responderBytes("image/png", png))
	c := nuevo(t, s)

	contenido, err := c.Documentos.DescargarKudeTicketImagen(t.Context(), "0180", OpcionesTicketImagen{Puntos: 384})
	if err != nil {
		t.Fatal(err)
	}

	if string(contenido) != string(png) {
		t.Errorf("contenido = %q", contenido)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/documento-electronico/0180/kude/ticket/imagen" || l.query != "puntos=384" {
		t.Errorf("%s?%s", l.ruta, l.query)
	}
	if !strings.HasPrefix(l.headers.Get("Accept"), "image/png") {
		t.Errorf("Accept = %q", l.headers.Get("Accept"))
	}
}

func TestCiudadesDeDepartamento(t *testing.T) {
	s := servidor(t, responderJSON(200, map[string]any{"data": []any{map[string]any{"ciudadId": 1, "nombre": "ASUNCION (DISTRITO)"}}}))
	c := nuevo(t, s)

	ciudades, err := c.Catalogos.CiudadesDeDepartamento(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(ciudades) != 1 || ciudades[0].Nombre != "ASUNCION (DISTRITO)" {
		t.Errorf("ciudades = %+v", ciudades)
	}
	l := s.registradas()[0]
	if l.ruta != "/api/v1/geografia/departamentos/1/ciudades" || l.headers.Get("Authorization") != "" {
		t.Errorf("%s auth=%q", l.ruta, l.headers.Get("Authorization"))
	}
}
