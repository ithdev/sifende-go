// Package sifende es el SDK de Sifende para Go: emití facturas electrónicas, notas de crédito y eventos de SIFEN,
// verificá webhooks y calculá totales con las mismas reglas que la API.
//
// Los montos, cantidades y tasas son [Decimal]: aritmética exacta, sin pasar por float. Cada operación tributaria
// viaja con Idempotency-Key, así que reintentar nunca duplica un documento.
//
// La referencia completa de la API está en https://sifende.com.py/docs.
package sifende

//go:generate go run ./internal/generar
