package sifende

import (
	"bytes"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Decimal es un número decimal exacto: un entero y una escala, como BigDecimal en la API. Los montos, cantidades y
// tasas viajan como Decimal para no perder precisión en un float.
//
// Las operaciones redondean con HALF_UP, igual que el backend. El valor cero es 0 con escala 0. Un Decimal es
// inmutable: las operaciones devuelven uno nuevo.
type Decimal struct {
	unidades *big.Int
	escala   int32
}

var formatoDecimal = regexp.MustCompile(`^([+-]?)(\d*)(?:\.(\d*))?(?:[eE]([+-]?\d+))?$`)

var diez = big.NewInt(10)

// NuevoDecimal devuelve el entero n como Decimal: NuevoDecimal(150000) es 150000.
func NuevoDecimal(n int64) Decimal {
	return Decimal{unidades: big.NewInt(n)}
}

// ParsearDecimal lee un decimal como "19.99", "-0.5" o "1e3". Conserva la escala del texto: "1.50" tiene escala 2.
func ParsearDecimal(texto string) (Decimal, error) {
	texto = strings.TrimSpace(texto)
	partes := formatoDecimal.FindStringSubmatch(texto)
	if partes == nil || (partes[2] == "" && partes[3] == "") {
		return Decimal{}, fmt.Errorf("sifende: decimal inválido: %q", texto)
	}
	signo, entera, fraccion, exponente := partes[1], partes[2], partes[3], partes[4]
	unidades, _ := new(big.Int).SetString(entera+fraccion, 10)
	if unidades == nil {
		unidades = new(big.Int)
	}
	exp := int64(0)
	if exponente != "" {
		var err error
		if exp, err = strconv.ParseInt(exponente, 10, 32); err != nil {
			return Decimal{}, fmt.Errorf("sifende: exponente fuera de rango: %q", texto)
		}
	}
	escala := int64(len(fraccion)) - exp
	if escala < 0 {
		unidades.Mul(unidades, potencia(int32(-escala)))
		escala = 0
	}
	if escala > 1<<20 {
		return Decimal{}, fmt.Errorf("sifende: escala fuera de rango: %q", texto)
	}
	if signo == "-" {
		unidades.Neg(unidades)
	}
	return Decimal{unidades: unidades, escala: int32(escala)}, nil
}

// MustDecimal es ParsearDecimal para literales: entra en pánico si el texto no es un decimal.
func MustDecimal(texto string) Decimal {
	d, err := ParsearDecimal(texto)
	if err != nil {
		panic(err)
	}
	return d
}

// DecimalDeFloat convierte un float64 con su representación decimal más corta: 0.1 es 0.1, no
// 0.1000000000000000055….
func DecimalDeFloat(f float64) (Decimal, error) {
	return ParsearDecimal(strconv.FormatFloat(f, 'g', -1, 64))
}

func (d Decimal) int() *big.Int {
	if d.unidades == nil {
		return new(big.Int)
	}
	return d.unidades
}

// Escala es la cantidad de dígitos después del punto.
func (d Decimal) Escala() int32 { return d.escala }

func (d Decimal) Sumar(otro Decimal) Decimal {
	a, b, escala := alinear(d, otro)
	return Decimal{unidades: a.Add(a, b), escala: escala}
}

func (d Decimal) Restar(otro Decimal) Decimal {
	a, b, escala := alinear(d, otro)
	return Decimal{unidades: a.Sub(a, b), escala: escala}
}

func (d Decimal) Multiplicar(otro Decimal) Decimal {
	return Decimal{unidades: new(big.Int).Mul(d.int(), otro.int()), escala: d.escala + otro.escala}
}

// Dividir devuelve d/divisor con la escala indicada, redondeado HALF_UP. Entra en pánico si el divisor es cero.
func (d Decimal) Dividir(divisor Decimal, escala int32) Decimal {
	if divisor.int().Sign() == 0 {
		panic("sifende: división por cero")
	}
	numerador := new(big.Int).Set(d.int())
	denominador := new(big.Int).Set(divisor.int())
	exponente := divisor.escala + escala - d.escala
	if exponente >= 0 {
		numerador.Mul(numerador, potencia(exponente))
	} else {
		denominador.Mul(denominador, potencia(-exponente))
	}
	return Decimal{unidades: dividirMitadArriba(numerador, denominador), escala: escala}
}

// ConEscala cambia la escala, redondeando HALF_UP si la reduce.
func (d Decimal) ConEscala(escala int32) Decimal {
	if escala >= d.escala {
		return Decimal{unidades: new(big.Int).Mul(d.int(), potencia(escala-d.escala)), escala: escala}
	}
	return Decimal{unidades: dividirMitadArriba(d.int(), potencia(d.escala-escala)), escala: escala}
}

// Resto es el resto de la división truncada, con el signo de d (como BigDecimal.remainder).
func (d Decimal) Resto(divisor Decimal) Decimal {
	a, b, escala := alinear(d, divisor)
	return Decimal{unidades: a.Rem(a, b), escala: escala}
}

// Comparar devuelve -1, 0 o 1 según d sea menor, igual o mayor que otro, sin importar la escala: 1.50 y 1.5 son
// iguales.
func (d Decimal) Comparar(otro Decimal) int {
	a, b, _ := alinear(d, otro)
	return a.Cmp(b)
}

func (d Decimal) EsCero() bool { return d.int().Sign() == 0 }

func (d Decimal) Abs() Decimal {
	return Decimal{unidades: new(big.Int).Abs(d.int()), escala: d.escala}
}

// String escribe el decimal con su escala, sin exponente: "1057450", "60.98", "-0.50".
func (d Decimal) String() string {
	u := d.int()
	digitos := new(big.Int).Abs(u).String()
	if relleno := int(d.escala) + 1 - len(digitos); relleno > 0 {
		digitos = strings.Repeat("0", relleno) + digitos
	}
	corte := len(digitos) - int(d.escala)
	texto := digitos[:corte]
	if d.escala > 0 {
		texto += "." + digitos[corte:]
	}
	if u.Sign() < 0 {
		texto = "-" + texto
	}
	return texto
}

// Float64 devuelve la aproximación float64 más cercana. Para mostrar o comparar montos usá String o Comparar.
func (d Decimal) Float64() float64 {
	f, _ := strconv.ParseFloat(d.String(), 64)
	return f
}

// MarshalJSON escribe el decimal como número JSON, con todos sus dígitos.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalJSON acepta un número JSON o un texto con un número.
func (d *Decimal) UnmarshalJSON(datos []byte) error {
	datos = bytes.TrimSpace(datos)
	if string(datos) == "null" {
		return nil
	}
	if len(datos) > 0 && datos[0] == '"' {
		texto, err := strconv.Unquote(string(datos))
		if err != nil {
			return err
		}
		datos = []byte(texto)
	}
	leido, err := ParsearDecimal(string(datos))
	if err != nil {
		return err
	}
	*d = leido
	return nil
}

func alinear(a, b Decimal) (*big.Int, *big.Int, int32) {
	escala := max(a.escala, b.escala)
	x := new(big.Int).Mul(a.int(), potencia(escala-a.escala))
	y := new(big.Int).Mul(b.int(), potencia(escala-b.escala))
	return x, y, escala
}

func potencia(exponente int32) *big.Int {
	return new(big.Int).Exp(diez, big.NewInt(int64(exponente)), nil)
}

func dividirMitadArriba(numerador, denominador *big.Int) *big.Int {
	cociente, resto := new(big.Int).QuoRem(numerador, denominador, new(big.Int))
	if resto.Sign() == 0 {
		return cociente
	}
	doble := new(big.Int).Abs(resto)
	doble.Lsh(doble, 1)
	if doble.Cmp(new(big.Int).Abs(denominador)) < 0 {
		return cociente
	}
	if (numerador.Sign() < 0) != (denominador.Sign() < 0) {
		return cociente.Sub(cociente, big.NewInt(1))
	}
	return cociente.Add(cociente, big.NewInt(1))
}
