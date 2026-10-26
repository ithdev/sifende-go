// Command generar escribe tipos_gen.go a partir del OpenAPI de la API de Sifende.
//
// Sólo toma la forma de los datos: propiedades, tipos, enumeraciones y obligatoriedad. Las reglas de validación
// condicionales del spec (if/then, not, oneOf entre campos) las aplica la API al emitir.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const specPorDefecto = "https://api.sifende.com.py/v3/api-docs"

// Esquemas que no se generan: la base abstracta del discriminador y su ítem genérico. Cada solicitud concreta
// redefine los ítems.
var omitidos = map[string]bool{"DocumentoElectronicoRequest": true, "ItemDocumento": true}

// Nombres de las enumeraciones que comparten nombre de propiedad con otra de valores distintos. La clave es el primer
// valor en orden alfabético.
var nombresEnum = map[string]string{
	"AUTOFACTURA_ELECTRONICA":     "TipoDocumento",
	"CONSTANCIA_ELECTRONICA":      "TipoDocumentoAsociado",
	"CARNET_DE_RESIDENCIA":        "TipoDocumentoIdentidad",
	"CONTADO":                     "CondicionOperacion",
	"CONSTANCIA_MICROPRODUCTORES": "TipoConstancia",
	"CONTRIBUYENTE":               "Naturaleza",
	"EXTRANJERO":                  "NaturalezaVendedor",
	"PERSONA_FISICA":              "TipoContribuyente",
	"B2B":                         "TipoOperacion",
	"VENTA_AL_CONSUMIDOR_FINAL":   "TipoOperacionVehiculo",
	"CANCELADA":                   "EstadoEntregaWebhook",
	"documento.aprobado":          "TipoEventoWebhook",
	"DIAS":                        "UnidadPlazo",
	"CARGA_PELIGROSA":             "CaracteristicaCarga",
	"AEREO":                       "ModalidadTransporte",
}

var siglas = map[string]bool{
	"id": true, "url": true, "uri": true, "cdc": true, "ruc": true, "iva": true, "qr": true, "xml": true,
	"pdf": true, "uuid": true, "http": true, "api": true, "gtin": true, "ncm": true, "dncp": true, "pyg": true,
	"dv": true, "isc": true,
}

func main() {
	spec := os.Getenv("SIFENDE_OPENAPI")
	if spec == "" {
		spec = specPorDefecto
	}
	salida := "tipos_gen.go"
	if len(os.Args) > 1 {
		salida = os.Args[1]
	}
	if err := ejecutar(spec, salida); err != nil {
		fmt.Fprintln(os.Stderr, "generar:", err)
		os.Exit(1)
	}
}

func ejecutar(spec, salida string) error {
	crudo, err := leer(spec)
	if err != nil {
		return err
	}
	raiz, err := decodificar(crudo)
	if err != nil {
		return fmt.Errorf("el spec no es JSON válido: %w", err)
	}
	g, err := nuevoGenerador(raiz.(*objeto))
	if err != nil {
		return err
	}
	codigo, err := g.generar()
	if err != nil {
		return err
	}
	formateado, err := format.Source(codigo)
	if err != nil {
		return fmt.Errorf("gofmt: %w\n%s", err, codigo)
	}
	return os.WriteFile(salida, formateado, 0o644)
}

func leer(spec string) ([]byte, error) {
	if !strings.HasPrefix(spec, "http://") && !strings.HasPrefix(spec, "https://") {
		return os.ReadFile(spec)
	}
	respuesta, err := http.Get(spec)
	if err != nil {
		return nil, err
	}
	defer respuesta.Body.Close()
	if respuesta.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s respondió %d", spec, respuesta.StatusCode)
	}
	return io.ReadAll(respuesta.Body)
}

// objeto conserva el orden de las claves: el orden de las propiedades del spec es el de los campos generados.
type objeto struct {
	claves  []string
	valores map[string]any
}

func (o *objeto) obj(clave string) *objeto {
	if o == nil {
		return nil
	}
	v, _ := o.valores[clave].(*objeto)
	return v
}

func (o *objeto) texto(clave string) string {
	if o == nil {
		return ""
	}
	v, _ := o.valores[clave].(string)
	return v
}

func (o *objeto) lista(clave string) []any {
	if o == nil {
		return nil
	}
	v, _ := o.valores[clave].([]any)
	return v
}

func (o *objeto) tiene(clave string) bool {
	if o == nil {
		return false
	}
	_, ok := o.valores[clave]
	return ok
}

func decodificar(crudo []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(crudo))
	dec.UseNumber()
	return valor(dec)
}

func valor(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &objeto{valores: map[string]any{}}
			for dec.More() {
				clave, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := valor(dec)
				if err != nil {
					return nil, err
				}
				o.claves = append(o.claves, clave.(string))
				o.valores[clave.(string)] = v
			}
			_, err = dec.Token()
			return o, err
		case '[':
			var l []any
			for dec.More() {
				v, err := valor(dec)
				if err != nil {
					return nil, err
				}
				l = append(l, v)
			}
			_, err = dec.Token()
			return l, err
		}
		return nil, fmt.Errorf("delimitador inesperado %v", t)
	default:
		return t, nil
	}
}

type propiedad struct {
	nombre      string
	esquema     *objeto
	obligatoria bool
}

type esquema struct {
	nombre      string
	descripcion string
	propiedades []propiedad
	// Valor fijo del discriminador tipoDocumento, si el esquema es una solicitud de emisión.
	discriminador string
}

type usoEnum struct {
	esquema, propiedad string
	valores            []string
}

type generador struct {
	esquemas   map[string]*objeto
	planos     map[string]*esquema
	entrada    map[string]bool
	usos       []usoEnum
	tipoDeUso  map[string]string // esquema.propiedad → tipo
	enums      map[string][]string
	ordenEnums []string
	inline     []*esquema
	discrimina map[string]string // esquema → valor
}

func nuevoGenerador(raiz *objeto) (*generador, error) {
	componentes := raiz.obj("components").obj("schemas")
	if componentes == nil {
		return nil, errors.New("el spec no tiene components.schemas")
	}
	g := &generador{
		esquemas:   map[string]*objeto{},
		planos:     map[string]*esquema{},
		entrada:    map[string]bool{},
		tipoDeUso:  map[string]string{},
		enums:      map[string][]string{},
		discrimina: map[string]string{},
	}
	for _, nombre := range componentes.claves {
		g.esquemas[nombre] = componentes.obj(nombre)
	}
	base := g.esquemas["DocumentoElectronicoRequest"]
	mapeo := base.obj("discriminator").obj("mapping")
	if mapeo == nil {
		return nil, errors.New("DocumentoElectronicoRequest no tiene el mapeo del discriminador")
	}
	for _, valor := range mapeo.claves {
		g.discrimina[nombreRef(mapeo.texto(valor))] = valor
	}

	for _, nombre := range componentes.claves {
		plano, err := g.aplanar(nombre, g.esquemas[nombre])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", nombre, err)
		}
		plano.discriminador = g.discrimina[nombre]
		g.planos[nombre] = plano
	}
	g.marcarEntrada(raiz.obj("paths"))
	g.resolverEnums()
	return g, nil
}

func nombreRef(ref string) string {
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

// aplanar junta las propiedades del esquema, de sus $ref y de los bloques allOf que sólo declaran propiedades.
func (g *generador) aplanar(nombre string, s *objeto) (*esquema, error) {
	plano := &esquema{nombre: nombre, descripcion: s.texto("description")}
	obligatorias := map[string]bool{}
	indice := map[string]int{}

	var juntar func(s *objeto) error
	juntar = func(s *objeto) error {
		if ref := s.texto("$ref"); ref != "" {
			return juntar(g.esquemas[nombreRef(ref)])
		}
		for _, parte := range s.lista("allOf") {
			p := parte.(*objeto)
			if p.tiene("$ref") || soloPropiedades(p) {
				if err := juntar(p); err != nil {
					return err
				}
			}
		}
		for _, r := range s.lista("required") {
			obligatorias[r.(string)] = true
		}
		props := s.obj("properties")
		if props == nil {
			return nil
		}
		for _, clave := range props.claves {
			ps := props.obj(clave)
			// Un bloque {"const": …} sólo fija el valor (el discriminador): no redefine el tipo del campo.
			if len(ps.claves) == 1 && ps.tiene("const") {
				if _, existe := indice[clave]; existe {
					continue
				}
			}
			if i, existe := indice[clave]; existe {
				plano.propiedades[i].esquema = ps
				continue
			}
			indice[clave] = len(plano.propiedades)
			plano.propiedades = append(plano.propiedades, propiedad{nombre: clave, esquema: ps})
		}
		return nil
	}
	if err := juntar(s); err != nil {
		return nil, err
	}
	// {"type": "null"} prohíbe un campo heredado de la base: no se genera.
	plano.propiedades = slices.DeleteFunc(plano.propiedades, func(p propiedad) bool {
		return len(p.esquema.claves) == 1 && p.esquema.texto("type") == "null"
	})
	for i := range plano.propiedades {
		plano.propiedades[i].obligatoria = obligatorias[plano.propiedades[i].nombre]
		g.registrarEnums(nombre, plano.propiedades[i].nombre, plano.propiedades[i].esquema)
	}
	return plano, nil
}

func soloPropiedades(p *objeto) bool {
	for _, clave := range p.claves {
		switch clave {
		case "properties", "type", "description", "required":
		default:
			return false
		}
	}
	return p.tiene("properties")
}

func (g *generador) registrarEnums(esquema, propiedad string, ps *objeto) {
	if ps == nil {
		return
	}
	if ps.tiene("items") {
		g.registrarEnums(esquema, propiedad, ps.obj("items"))
		return
	}
	if valores := ps.lista("enum"); valores != nil {
		uso := usoEnum{esquema: esquema, propiedad: propiedad}
		for _, v := range valores {
			uso.valores = append(uso.valores, v.(string))
		}
		g.usos = append(g.usos, uso)
	}
}

func clave(valores []string) string {
	ordenados := slices.Clone(valores)
	sort.Strings(ordenados)
	return strings.Join(ordenados, "\x00")
}

func incluido(a, b []string) bool {
	for _, v := range a {
		if !slices.Contains(b, v) {
			return false
		}
	}
	return true
}

// resolverEnums da un tipo a cada conjunto de valores. Dentro de una misma propiedad, un conjunto contenido en otro
// usa el tipo del mayor.
func (g *generador) resolverEnums() {
	porPropiedad := map[string][][]string{}
	for _, u := range g.usos {
		porPropiedad[u.propiedad] = append(porPropiedad[u.propiedad], u.valores)
	}
	canonico := func(u usoEnum) []string {
		mayor := u.valores
		for _, otro := range porPropiedad[u.propiedad] {
			if len(otro) > len(mayor) && incluido(mayor, otro) {
				mayor = otro
			}
		}
		return mayor
	}

	propiedadesDe := map[string][]string{}
	valoresDe := map[string][]string{}
	var ordenClaves []string
	for _, u := range g.usos {
		c := canonico(u)
		k := clave(c)
		if _, visto := valoresDe[k]; !visto {
			ordenClaves = append(ordenClaves, k)
			valoresDe[k] = c
		}
		propiedadesDe[k] = append(propiedadesDe[k], u.propiedad)
	}

	nombreDe := map[string]string{}
	for _, k := range ordenClaves {
		ordenados := slices.Clone(valoresDe[k])
		sort.Strings(ordenados)
		nombre, ok := nombresEnum[ordenados[0]]
		if !ok {
			props := propiedadesDe[k]
			sort.Slice(props, func(i, j int) bool {
				if len(props[i]) != len(props[j]) {
					return len(props[i]) < len(props[j])
				}
				return props[i] < props[j]
			})
			nombre = exportado(props[0])
		}
		nombreDe[k] = nombre
	}
	usados := map[string]string{}
	for _, k := range ordenClaves {
		nombre := nombreDe[k]
		if previo, repetido := usados[nombre]; repetido {
			panic(fmt.Sprintf("dos enumeraciones distintas se llamarían %s: agregá una entrada a nombresEnum para %q o %q",
				nombre, strings.Split(previo, "\x00")[0], strings.Split(k, "\x00")[0]))
		}
		usados[nombre] = k
		g.enums[nombre] = valoresDe[k]
		g.ordenEnums = append(g.ordenEnums, nombre)
	}
	for _, u := range g.usos {
		g.tipoDeUso[u.esquema+"."+u.propiedad] = nombreDe[clave(canonico(u))]
	}
}

// marcarEntrada señala los esquemas que viajan en el cuerpo de una solicitud: sus campos opcionales usan punteros
// para distinguir «ausente» de cero. Los de respuesta usan valores.
func (g *generador) marcarEntrada(paths *objeto) {
	var visitar func(s *objeto)
	visitar = func(s *objeto) {
		if s == nil {
			return
		}
		if ref := s.texto("$ref"); ref != "" {
			nombre := nombreRef(ref)
			if g.entrada[nombre] {
				return
			}
			g.entrada[nombre] = true
			visitar(g.esquemas[nombre])
			return
		}
		for _, clave := range s.claves {
			switch v := s.valores[clave].(type) {
			case *objeto:
				visitar(v)
			case []any:
				for _, e := range v {
					if o, ok := e.(*objeto); ok {
						visitar(o)
					}
				}
			}
		}
	}
	for _, ruta := range paths.claves {
		for _, metodo := range paths.obj(ruta).claves {
			visitar(paths.obj(ruta).obj(metodo).obj("requestBody"))
		}
	}
}

func (g *generador) generar() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/generar from the Sifende OpenAPI. DO NOT EDIT.\n\n")
	b.WriteString("package sifende\n\nimport \"encoding/json\"\n\n")

	for _, nombre := range g.ordenEnums {
		g.escribirEnum(&b, nombre, g.enums[nombre])
	}

	nombres := make([]string, 0, len(g.planos))
	for nombre := range g.planos {
		if !omitidos[nombre] {
			nombres = append(nombres, nombre)
		}
	}
	sort.Strings(nombres)
	for _, nombre := range nombres {
		if err := g.escribirStruct(&b, g.planos[nombre], g.entrada[nombre]); err != nil {
			return nil, fmt.Errorf("%s: %w", nombre, err)
		}
	}
	for i := 0; i < len(g.inline); i++ {
		if err := g.escribirStruct(&b, g.inline[i], true); err != nil {
			return nil, err
		}
	}
	g.escribirSolicitudes(&b, nombres)
	return b.Bytes(), nil
}

func (g *generador) escribirEnum(b *bytes.Buffer, nombre string, valores []string) {
	fmt.Fprintf(b, "// %s es una enumeración de la API. Un valor fuera de las constantes se envía tal cual y la API lo valida.\n", nombre)
	fmt.Fprintf(b, "type %s string\n\nconst (\n", nombre)
	vistos := map[string]string{}
	for _, v := range valores {
		constante := nombre + exportadoValor(v)
		if previo, repetido := vistos[constante]; repetido {
			panic(fmt.Sprintf("%s: los valores %q y %q generan la misma constante %s", nombre, previo, v, constante))
		}
		vistos[constante] = v
		fmt.Fprintf(b, "\t%s %s = %q\n", constante, nombre, v)
	}
	b.WriteString(")\n\n")
}

func (g *generador) escribirStruct(b *bytes.Buffer, s *esquema, entrada bool) error {
	comentario(b, "", s.nombre, s.descripcion)
	fmt.Fprintf(b, "type %s struct {\n", s.nombre)
	for _, p := range s.propiedades {
		if s.discriminador != "" && p.nombre == "tipoDocumento" {
			continue
		}
		tipo, err := g.tipo(s.nombre, p.nombre, p.esquema)
		if err != nil {
			return fmt.Errorf("%s: %w", p.nombre, err)
		}
		nulable := esNulable(p.esquema)
		omitir := !p.obligatoria || nulable
		if entrada && omitir && necesitaPuntero(tipo, g) {
			tipo = "*" + tipo
		}
		// En una respuesta, un objeto anidado es puntero: nil si no vino.
		if !entrada {
			if _, esStruct := g.planos[tipo]; esStruct {
				tipo = "*" + tipo
			}
		}
		etiqueta := p.nombre
		if omitir || !entrada {
			etiqueta += ",omitempty"
		}
		descripcion := p.esquema.texto("description")
		if descripcion == "" {
			descripcion = descripcionRef(g, p.esquema)
		}
		comentario(b, "\t", "", descripcion)
		if p.esquema.valores["deprecated"] == true {
			b.WriteString("\t//\n\t// Deprecated: la API lo mantiene por compatibilidad.\n")
		}
		fmt.Fprintf(b, "\t%s %s `json:%q`\n", exportado(p.nombre), tipo, etiqueta)
	}
	b.WriteString("}\n\n")
	return nil
}

func descripcionRef(g *generador, ps *objeto) string {
	if ref := ps.texto("$ref"); ref != "" {
		return g.esquemas[nombreRef(ref)].texto("description")
	}
	return ""
}

func comentario(b *bytes.Buffer, sangria, nombre, texto string) {
	texto = strings.TrimSpace(texto)
	if texto == "" {
		if nombre != "" {
			fmt.Fprintf(b, "%s// %s es un objeto de la API de Sifende.\n", sangria, nombre)
		}
		return
	}
	for i, linea := range strings.Split(texto, "\n") {
		if i == 0 && nombre != "" {
			linea = nombre + ": " + linea
		}
		fmt.Fprintf(b, "%s// %s\n", sangria, strings.TrimRight(linea, " "))
	}
}

func esNulable(ps *objeto) bool {
	if l := ps.lista("type"); l != nil {
		return slices.Contains(l, any("null"))
	}
	for _, parte := range ps.lista("anyOf") {
		if parte.(*objeto).texto("type") == "null" {
			return true
		}
	}
	return false
}

// necesitaPuntero indica si un campo opcional necesita puntero para enviar un cero explícito. Los textos y las
// enumeraciones no: la API no admite un texto vacío donde el campo es opcional.
func necesitaPuntero(tipo string, g *generador) bool {
	if strings.HasPrefix(tipo, "[]") || strings.HasPrefix(tipo, "map[") || tipo == "string" || tipo == "any" ||
		tipo == "json.RawMessage" {
		return false
	}
	if _, esEnum := g.enums[tipo]; esEnum {
		return false
	}
	return true
}

func (g *generador) tipo(esquema, propiedad string, ps *objeto) (string, error) {
	if ps == nil {
		return "any", nil
	}
	if ref := ps.texto("$ref"); ref != "" {
		return nombreRef(ref), nil
	}
	for _, combinacion := range []string{"allOf", "anyOf", "oneOf"} {
		partes := ps.lista(combinacion)
		if partes == nil {
			continue
		}
		var refs []string
		for _, parte := range partes {
			p := parte.(*objeto)
			if r := p.texto("$ref"); r != "" {
				refs = append(refs, nombreRef(r))
			}
		}
		if len(refs) == 1 {
			return refs[0], nil
		}
		if !ps.tiene("type") {
			return "", fmt.Errorf("%s sin un único $ref no está soportado", combinacion)
		}
	}
	if ps.tiene("enum") {
		if nombre, ok := g.tipoDeUso[esquema+"."+propiedad]; ok {
			return nombre, nil
		}
	}
	tipo := ps.texto("type")
	if l := ps.lista("type"); l != nil {
		for _, t := range l {
			if t != "null" {
				tipo = t.(string)
			}
		}
	}
	switch tipo {
	case "string":
		return "string", nil
	case "boolean":
		return "bool", nil
	case "number":
		return "Decimal", nil
	case "integer":
		switch ps.texto("format") {
		case "int32":
			return "int32", nil
		case "int64":
			return "int64", nil
		default:
			return "int", nil
		}
	case "array":
		if !ps.tiene("items") {
			return "[]any", nil
		}
		elemento, err := g.tipo(esquema, propiedad, ps.obj("items"))
		if err != nil {
			return "", err
		}
		return "[]" + elemento, nil
	case "object", "":
		if adicionales := ps.obj("additionalProperties"); adicionales != nil {
			valor, err := g.tipo(esquema, propiedad, adicionales)
			if err != nil {
				return "", err
			}
			return "map[string]" + valor, nil
		}
		if ps.tiene("properties") {
			anidado, err := g.aplanar(esquema+exportado(propiedad), ps)
			if err != nil {
				return "", err
			}
			g.inline = append(g.inline, anidado)
			return anidado.nombre, nil
		}
		if ps.tiene("const") {
			return "string", nil
		}
		return "json.RawMessage", nil
	}
	return "", fmt.Errorf("tipo %q no soportado", tipo)
}

func (g *generador) escribirSolicitudes(b *bytes.Buffer, nombres []string) {
	b.WriteString("// SolicitudEmision es el cuerpo de documentos.Emitir. La implementan los punteros a ")
	var tipos []string
	for _, n := range nombres {
		if g.discrimina[n] != "" {
			tipos = append(tipos, n)
		}
	}
	b.WriteString(strings.Join(tipos, ", "))
	b.WriteString(".\ntype SolicitudEmision interface {\n\tTipoDocumento() TipoDocumento\n\tsolicitudEmision()\n}\n\n")
	for _, n := range tipos {
		constante := "TipoDocumento" + exportadoValor(g.discrimina[n])
		fmt.Fprintf(b, "// TipoDocumento devuelve %s: el valor que %s envía en tipoDocumento.\n", constante, n)
		fmt.Fprintf(b, "func (*%s) TipoDocumento() TipoDocumento { return %s }\n\n", n, constante)
		fmt.Fprintf(b, "func (*%s) solicitudEmision() {}\n\n", n)
		fmt.Fprintf(b, "// MarshalJSON agrega tipoDocumento al cuerpo.\nfunc (s %s) MarshalJSON() ([]byte, error) {\n", n)
		fmt.Fprintf(b, "\ttype sinMetodos %s\n", n)
		b.WriteString("\treturn json.Marshal(struct {\n\t\tTipoDocumento TipoDocumento `json:\"tipoDocumento\"`\n\t\tsinMetodos\n")
		fmt.Fprintf(b, "\t}{%s, sinMetodos(s)})\n}\n\n", constante)
	}
}

// exportado convierte un nombre camelCase en un identificador exportado, con las siglas en mayúsculas: tasaIVA →
// TasaIVA, kudeUrl → KudeURL, cdc → CDC.
func exportado(nombre string) string {
	var palabras []string
	runas := []rune(nombre)
	inicio := 0
	for i := 1; i < len(runas); i++ {
		anterior, actual := runas[i-1], runas[i]
		siguienteMinuscula := i+1 < len(runas) && unicode.IsLower(runas[i+1])
		if unicode.IsUpper(actual) && (unicode.IsLower(anterior) || unicode.IsDigit(anterior) ||
			(unicode.IsUpper(anterior) && siguienteMinuscula)) {
			palabras = append(palabras, string(runas[inicio:i]))
			inicio = i
		}
	}
	palabras = append(palabras, string(runas[inicio:]))
	var b strings.Builder
	for _, p := range palabras {
		if siglas[strings.ToLower(p)] {
			b.WriteString(strings.ToUpper(p))
			continue
		}
		r := []rune(p)
		b.WriteString(strings.ToUpper(string(r[0])) + string(r[1:]))
	}
	return b.String()
}

// exportadoValor convierte un valor de enumeración en sufijo de constante: GRAVADO_PARCIAL → GravadoParcial,
// documento.aprobado → DocumentoAprobado. Los códigos de hasta tres caracteres (PYG, IVA) y las partes con dígitos
// (B2B) quedan en mayúsculas.
func exportadoValor(valor string) string {
	// DEV es una palabra, no una sigla: AmbienteDev junto a AmbienteProd.
	if valor == "DEV" {
		return "Dev"
	}
	partes := strings.FieldsFunc(valor, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for _, p := range partes {
		r := []rune(p)
		switch {
		case strings.ToUpper(p) == p && (len(r) <= 3 && len(partes) == 1 || strings.ContainsAny(p, "0123456789")):
			b.WriteString(p)
		case strings.ToUpper(p) == p:
			b.WriteString(string(r[0]) + strings.ToLower(string(r[1:])))
		default:
			b.WriteString(strings.ToUpper(string(r[0])) + string(r[1:]))
		}
	}
	return b.String()
}
