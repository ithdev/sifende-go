package sifende

import "fmt"

// ItemParaTotales son los campos de un ítem que intervienen en el total. Tienen los mismos nombres y tipos que en
// ItemDTO.
type ItemParaTotales struct {
	PrecioUnitario                Decimal
	Cantidad                      Decimal
	AfectacionTributaria          AfectacionTributaria
	TasaIVA                       *int32
	PropIVA                       *Decimal
	TipoCambio                    *Decimal
	DescuentoParticular           *Decimal
	DescuentoParticularPorcentaje *Decimal
	AnticipoParticular            *Decimal
}

// DocumentoParaTotales son los campos de un documento que intervienen en el total.
type DocumentoParaTotales struct {
	// MonedaOperacion vacía es PYG.
	MonedaOperacion           Moneda
	TipoCambio                *Decimal
	DescuentoGlobalPorcentaje *Decimal
	AnticipoGlobalPorcentaje  *Decimal
	Comision                  *Decimal
	Items                     []ItemParaTotales
}

// Totales es el resultado de CalcularTotales.
type Totales struct {
	// TotalOperacion es la suma de los ítems antes del redondeo (F014-F017).
	TotalOperacion Decimal
	// Redondeo es el redondeo oficial de la SET (F013): en PYG, lo que se descuenta para llegar a un múltiplo de 50.
	Redondeo Decimal
	// TotalGeneral es el total a cobrar (F014): el MontoPago de una operación al contado, en la moneda de la
	// operación.
	TotalGeneral Decimal
	// TotalGuaranies es el total en guaraníes (F023); 0 en PYG.
	TotalGuaranies Decimal
}

var (
	cien      = NuevoDecimal(100)
	cincuenta = NuevoDecimal(50)
	diezMil   = NuevoDecimal(10000)
)

// Igual que FiscalTotalCalculator.GLOBAL_DISCOUNT_FOREIGN_SCALE.
const escalaDescuentoExtranjera = 8

// CalcularTotales calcula los totales con las mismas reglas que Sifende valida al emitir, para que MontoPago
// coincida con el total redondeado sin un ida y vuelta a la API.
//
// Devuelve error si la operación es en moneda extranjera, no tiene tipo de cambio global y a un ítem le falta el
// suyo.
func CalcularTotales(documento DocumentoParaTotales) (Totales, error) {
	moneda := documento.MonedaOperacion
	if moneda == "" {
		moneda = MonedaPYG
	}
	esGuarani := moneda == MonedaPYG
	escala := int32(2)
	if esGuarani {
		escala = 0
	}
	cambioGlobal := documento.TipoCambio

	var totalOperacion, totalGuaraniesItems Decimal
	for i, item := range documento.Items {
		precio := item.PrecioUnitario
		var descuentoParticular, descuentoGlobal, anticipoGlobal *Decimal
		if item.DescuentoParticularPorcentaje != nil {
			descuentoParticular = porcentajeSobrePrecio(esGuarani, precio, *item.DescuentoParticularPorcentaje)
		} else {
			descuentoParticular = item.DescuentoParticular
		}
		if documento.DescuentoGlobalPorcentaje != nil {
			descuentoGlobal = porcentajeSobrePrecio(esGuarani, precio, *documento.DescuentoGlobalPorcentaje)
		}
		if documento.AnticipoGlobalPorcentaje != nil {
			anticipoGlobal = porcentajeSobrePrecio(esGuarani, precio, *documento.AnticipoGlobalPorcentaje)
		}

		precioNeto := precio
		for _, deduccion := range []*Decimal{descuentoParticular, descuentoGlobal, item.AnticipoParticular, anticipoGlobal} {
			if deduccion != nil {
				precioNeto = precioNeto.Restar(*deduccion)
			}
		}
		totalItem := precioNeto.Multiplicar(item.Cantidad).ConEscala(escala)

		if !esGuarani && cambioGlobal == nil && item.TipoCambio == nil {
			return Totales{}, fmt.Errorf("sifende: el ítem %d necesita TipoCambio: la operación en %s no tiene tipo de cambio global", i, moneda)
		}
		// Como FiscalTotalCalculator.aggregateItems: un ítem que no aporta queda fuera de ambos totales.
		if !aportaAlTotal(item) {
			continue
		}

		totalOperacion = totalOperacion.Sumar(aporteAlTotal(item, totalItem, escala))
		if !esGuarani && cambioGlobal == nil && item.TipoCambio != nil {
			totalGuaraniesItems = totalGuaraniesItems.Sumar(totalItem.Multiplicar(*item.TipoCambio))
		}
	}

	var redondeo Decimal
	if esGuarani {
		redondeo = totalOperacion.Resto(cincuenta)
	}
	var comision Decimal
	if documento.Comision != nil {
		comision = documento.Comision.ConEscala(escala)
	}
	totalGeneral := totalOperacion.Restar(redondeo).Sumar(comision)

	var totalGuaranies Decimal
	switch {
	case esGuarani:
	case cambioGlobal == nil:
		totalGuaranies = totalGuaraniesItems
	default:
		totalGuaranies = totalGeneral.Multiplicar(*cambioGlobal)
	}

	return Totales{
		TotalOperacion: totalOperacion,
		Redondeo:       redondeo.Abs(),
		TotalGeneral:   totalGeneral,
		TotalGuaranies: totalGuaranies,
	}, nil
}

func aportaAlTotal(item ItemParaTotales) bool {
	switch item.AfectacionTributaria {
	case AfectacionTributariaExento, AfectacionTributariaExonerado:
		return true
	case AfectacionTributariaGravado, AfectacionTributariaGravadoParcial:
		return tasaValida(item.TasaIVA)
	}
	return false
}

// aporteAlTotal es lo que suma un ítem que aporta al total.
func aporteAlTotal(item ItemParaTotales, totalItem Decimal, escala int32) Decimal {
	if item.AfectacionTributaria != AfectacionTributariaGravadoParcial || item.PropIVA == nil || !tasaValida(item.TasaIVA) {
		return totalItem
	}
	// NT013: el gravado parcial aporta base gravada + liquidación + base exenta, cada una redondeada.
	tasa := NuevoDecimal(int64(*item.TasaIVA))
	proporcion := *item.PropIVA
	denominador := diezMil.Sumar(tasa.Multiplicar(proporcion))
	base := cien.Multiplicar(totalItem).Multiplicar(proporcion).Dividir(denominador, escala)
	liquidacion := base.Multiplicar(tasa).Dividir(cien, escala)
	exenta := cien.Multiplicar(totalItem).Multiplicar(cien.Restar(proporcion)).Dividir(denominador, escala)
	return base.Sumar(liquidacion).Sumar(exenta)
}

func porcentajeSobrePrecio(esGuarani bool, precio, porcentaje Decimal) *Decimal {
	exacto := precio.Multiplicar(porcentaje).Dividir(cien, precio.escala+porcentaje.escala+2)
	escala := int32(escalaDescuentoExtranjera)
	if esGuarani {
		escala = 0
	}
	resultado := exacto.ConEscala(escala)
	return &resultado
}

func tasaValida(tasa *int32) bool {
	return tasa != nil && (*tasa == 5 || *tasa == 10)
}

func itemsParaTotales(items []ItemDTO) []ItemParaTotales {
	resultado := make([]ItemParaTotales, len(items))
	for i, item := range items {
		resultado[i] = ItemParaTotales{
			PrecioUnitario:                item.PrecioUnitario,
			Cantidad:                      item.Cantidad,
			AfectacionTributaria:          item.AfectacionTributaria,
			TasaIVA:                       item.TasaIVA,
			PropIVA:                       item.PropIVA,
			TipoCambio:                    item.TipoCambio,
			DescuentoParticular:           item.DescuentoParticular,
			DescuentoParticularPorcentaje: item.DescuentoParticularPorcentaje,
			AnticipoParticular:            item.AnticipoParticular,
		}
	}
	return resultado
}

// Totales calcula los totales de la factura con CalcularTotales.
func (f *FacturaElectronicaRequest) Totales() (Totales, error) {
	return CalcularTotales(DocumentoParaTotales{
		MonedaOperacion:           f.MonedaOperacion,
		TipoCambio:                f.TipoCambio,
		DescuentoGlobalPorcentaje: f.DescuentoGlobalPorcentaje,
		AnticipoGlobalPorcentaje:  f.AnticipoGlobalPorcentaje,
		Comision:                  f.Comision,
		Items:                     itemsParaTotales(f.Items),
	})
}

// Totales calcula los totales de la nota de crédito con CalcularTotales.
func (n *NotaCreditoElectronicaRequest) Totales() (Totales, error) {
	return CalcularTotales(DocumentoParaTotales{
		MonedaOperacion:           n.MonedaOperacion,
		TipoCambio:                n.TipoCambio,
		DescuentoGlobalPorcentaje: n.DescuentoGlobalPorcentaje,
		Comision:                  n.Comision,
		Items:                     itemsParaTotales(n.Items),
	})
}

// Totales calcula los totales de la nota de débito con CalcularTotales.
func (n *NotaDebitoElectronicaRequest) Totales() (Totales, error) {
	return CalcularTotales(DocumentoParaTotales{
		MonedaOperacion:           n.MonedaOperacion,
		TipoCambio:                n.TipoCambio,
		DescuentoGlobalPorcentaje: n.DescuentoGlobalPorcentaje,
		Comision:                  n.Comision,
		Items:                     itemsParaTotales(n.Items),
	})
}
