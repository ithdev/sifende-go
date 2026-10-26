package sifende

import "testing"

func d(texto string) *Decimal {
	v := MustDecimal(texto)
	return &v
}

func gravado(precio, cantidad string, tasa int32) ItemParaTotales {
	return ItemParaTotales{
		PrecioUnitario:       MustDecimal(precio),
		Cantidad:             MustDecimal(cantidad),
		AfectacionTributaria: AfectacionTributariaGravado,
		TasaIVA:              &tasa,
	}
}

func calcular(t *testing.T, documento DocumentoParaTotales) Totales {
	t.Helper()
	totales, err := CalcularTotales(documento)
	if err != nil {
		t.Fatal(err)
	}
	return totales
}

func igual(t *testing.T, campo string, obtenido Decimal, esperado string) {
	t.Helper()
	if obtenido.Comparar(MustDecimal(esperado)) != 0 {
		t.Errorf("%s = %s, se esperaba %s", campo, obtenido, esperado)
	}
}

func TestRedondeaElTotalEnGuaraniesAlMultiploDe50(t *testing.T) {
	totales := calcular(t, DocumentoParaTotales{Items: []ItemParaTotales{gravado("107437", "1", 10)}})

	igual(t, "TotalOperacion", totales.TotalOperacion, "107437")
	igual(t, "Redondeo", totales.Redondeo, "37")
	igual(t, "TotalGeneral", totales.TotalGeneral, "107400")
	igual(t, "TotalGuaranies", totales.TotalGuaranies, "0")
	if totales.TotalGeneral.String() != "107400" {
		t.Errorf("TotalGeneral.String() = %q", totales.TotalGeneral)
	}
}

func TestDescuentosPorItemYGlobales(t *testing.T) {
	conDescuento := gravado("11000", "2", 10)
	conDescuento.DescuentoParticular = d("500")
	exento := ItemParaTotales{
		PrecioUnitario:                MustDecimal("5250"),
		Cantidad:                      MustDecimal("3"),
		AfectacionTributaria:          AfectacionTributariaExento,
		TasaIVA:                       new(int32(0)),
		DescuentoParticularPorcentaje: d("12.5"),
	}

	totales := calcular(t, DocumentoParaTotales{DescuentoGlobalPorcentaje: d("10"), Items: []ItemParaTotales{conDescuento, exento}})

	// (11000 - 500 - 1100) * 2 = 18800; descuentos de 656.25 → 656 y 525 → (5250 - 656 - 525) * 3 = 12207
	igual(t, "TotalOperacion", totales.TotalOperacion, "31007")
	igual(t, "TotalGeneral", totales.TotalGeneral, "31000")
}

func TestCantidadesDecimales(t *testing.T) {
	totales := calcular(t, DocumentoParaTotales{Items: []ItemParaTotales{gravado("12345", "0.333", 5)}})

	// 12345 × 0.333 = 4110.885 → 4111
	igual(t, "TotalOperacion", totales.TotalOperacion, "4111")
	igual(t, "TotalGeneral", totales.TotalGeneral, "4100")
}

func TestGravadoParcial(t *testing.T) {
	item := gravado("100000", "1", 10)
	item.AfectacionTributaria = AfectacionTributariaGravadoParcial
	item.PropIVA = d("30")

	totales := calcular(t, DocumentoParaTotales{Items: []ItemParaTotales{item}})

	// denominador 10300: base 29126, IVA 2913, exenta 67961
	igual(t, "TotalOperacion", totales.TotalOperacion, "100000")
}

func TestIgnoraGravadosSinTasaValida(t *testing.T) {
	exonerado := ItemParaTotales{PrecioUnitario: MustDecimal("2000"), Cantidad: MustDecimal("1"), AfectacionTributaria: AfectacionTributariaExonerado}

	totales := calcular(t, DocumentoParaTotales{Items: []ItemParaTotales{gravado("1000", "1", 0), exonerado}})

	igual(t, "TotalOperacion", totales.TotalOperacion, "2000")
}

func TestMonedaExtranjeraConCambioGlobal(t *testing.T) {
	totales := calcular(t, DocumentoParaTotales{
		MonedaOperacion: MonedaUSD,
		TipoCambio:      d("7300.5"),
		Comision:        d("1.005"),
		Items:           []ItemParaTotales{gravado("19.99", "3", 10)},
	})

	igual(t, "Redondeo", totales.Redondeo, "0")
	igual(t, "TotalOperacion", totales.TotalOperacion, "59.97")
	igual(t, "TotalGeneral", totales.TotalGeneral, "60.98")
	igual(t, "TotalGuaranies", totales.TotalGuaranies, "445184.49")
}

func TestGuaraniesPorItemSinCambioGlobal(t *testing.T) {
	a := gravado("10", "1", 10)
	a.TipoCambio = d("7000")
	b := ItemParaTotales{PrecioUnitario: MustDecimal("5"), Cantidad: MustDecimal("2"), AfectacionTributaria: AfectacionTributariaExento, TipoCambio: d("7100")}

	totales := calcular(t, DocumentoParaTotales{MonedaOperacion: MonedaUSD, Items: []ItemParaTotales{a, b}})

	igual(t, "TotalGuaranies", totales.TotalGuaranies, "141000")
}

func TestGuaraniesPorItemIgnoraGravadosSinTasa(t *testing.T) {
	sinTasa := ItemParaTotales{PrecioUnitario: MustDecimal("100"), Cantidad: MustDecimal("1"), AfectacionTributaria: AfectacionTributariaGravado, TipoCambio: d("7000")}
	exento := ItemParaTotales{PrecioUnitario: MustDecimal("10"), Cantidad: MustDecimal("1"), AfectacionTributaria: AfectacionTributariaExento, TipoCambio: d("7000")}

	totales := calcular(t, DocumentoParaTotales{MonedaOperacion: MonedaUSD, Items: []ItemParaTotales{sinTasa, exento}})

	igual(t, "TotalOperacion", totales.TotalOperacion, "10")
	igual(t, "TotalGuaranies", totales.TotalGuaranies, "70000")
}

func TestExigeTipoCambioPorItemSinCambioGlobal(t *testing.T) {
	_, err := CalcularTotales(DocumentoParaTotales{MonedaOperacion: MonedaUSD, Items: []ItemParaTotales{gravado("10", "1", 10)}})
	if err == nil {
		t.Fatal("se esperaba un error")
	}
}

func TestTotalesDeLaFactura(t *testing.T) {
	f := factura()
	f.Items = append(f.Items, ItemDTO{
		Codigo:               "ENVIO",
		Descripcion:          "Envío",
		Cantidad:             NuevoDecimal(1),
		UnidadMedida:         "UNI",
		PrecioUnitario:       NuevoDecimal(25_030),
		AfectacionTributaria: AfectacionTributariaGravado,
		TasaIVA:              new(int32(10)),
	})

	totales, err := f.Totales()
	if err != nil {
		t.Fatal(err)
	}

	igual(t, "TotalGeneral", totales.TotalGeneral, "175000")
	igual(t, "Redondeo", totales.Redondeo, "30")
}
