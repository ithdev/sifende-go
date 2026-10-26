package sifende

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestParsearDecimal(t *testing.T) {
	casos := map[string]string{
		"150000": "150000", "19.99": "19.99", "1.50": "1.50", "-0.5": "-0.5", ".5": "0.5", "5.": "5",
		"1e3": "1000", "1.5e-3": "0.0015", "+7": "7", " 42 ": "42", "0.00": "0.00",
	}
	for texto, esperado := range casos {
		d, err := ParsearDecimal(texto)
		if err != nil {
			t.Errorf("%q: %v", texto, err)
			continue
		}
		if d.String() != esperado {
			t.Errorf("%q → %q, se esperaba %q", texto, d, esperado)
		}
	}
	for _, invalido := range []string{"", ".", "abc", "1,5", "1e", "--1", "1e99999999999"} {
		if _, err := ParsearDecimal(invalido); err == nil {
			t.Errorf("%q debería ser inválido", invalido)
		}
	}
}

func TestRedondeoMitadArriba(t *testing.T) {
	casos := []struct {
		valor    string
		escala   int32
		esperado string
	}{
		{"1.005", 2, "1.01"}, {"1.004", 2, "1.00"}, {"-1.005", 2, "-1.01"}, {"4110.885", 0, "4111"},
		{"656.25", 0, "656"}, {"2.5", 0, "3"}, {"-2.5", 0, "-3"}, {"7", 2, "7.00"},
	}
	for _, c := range casos {
		if got := MustDecimal(c.valor).ConEscala(c.escala).String(); got != c.esperado {
			t.Errorf("%s con escala %d = %s, se esperaba %s", c.valor, c.escala, got, c.esperado)
		}
	}
	if got := NuevoDecimal(2).Dividir(NuevoDecimal(3), 4).String(); got != "0.6667" {
		t.Errorf("2/3 = %s", got)
	}
	if got := NuevoDecimal(-2).Dividir(NuevoDecimal(3), 0).String(); got != "-1" {
		t.Errorf("-2/3 = %s", got)
	}
}

func TestRestoConSignoDelDividendo(t *testing.T) {
	if got := NuevoDecimal(-107437).Resto(NuevoDecimal(50)).String(); got != "-37" {
		t.Errorf("resto = %s", got)
	}
}

func TestDecimalCero(t *testing.T) {
	var cero Decimal
	if !cero.EsCero() || cero.String() != "0" || cero.Sumar(NuevoDecimal(5)).String() != "5" {
		t.Errorf("el valor cero de Decimal no se comporta como 0: %s", cero)
	}
}

func TestDecimalJSON(t *testing.T) {
	var v struct {
		Monto   Decimal  `json:"monto"`
		Texto   Decimal  `json:"texto"`
		Nulo    *Decimal `json:"nulo"`
		Ausente *Decimal `json:"ausente,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{"monto": 60.980, "texto": "19.99", "nulo": null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Monto.String() != "60.980" || v.Texto.String() != "19.99" || v.Nulo != nil {
		t.Errorf("leído = %+v", v)
	}
	crudo, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(crudo) != `{"monto":60.980,"texto":19.99,"nulo":null}` {
		t.Errorf("escrito = %s", crudo)
	}
	if err := json.Unmarshal([]byte(`{"monto": "1,5"}`), &v); err == nil {
		t.Error("un texto que no es número debería fallar")
	}
}

func TestDecimalDeFloat(t *testing.T) {
	d, err := DecimalDeFloat(0.1)
	if err != nil || d.String() != "0.1" {
		t.Errorf("0.1 → %s, %v", d, err)
	}
}

func ExampleDecimal() {
	precio := MustDecimal("19.99")
	total := precio.Multiplicar(NuevoDecimal(3))
	fmt.Println(total, total.ConEscala(0), total.Comparar(MustDecimal("59.970")))
	// Output: 59.97 60 0
}
