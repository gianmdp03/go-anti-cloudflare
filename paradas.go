package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	proxyURL   = "http://localhost:8080/proxy"
	outputFile = "paradas_mgp.json"
)

// Catálogo de líneas provisto
var lineas = []struct {
	Nombre string
	Codigo string
}{
	{"501", "93"},
	{"511", "98"},
	{"512", "99"},
	{"521", "100"},
	{"522", "101"},
	{"523", "102"},
	{"525", "103"},
	{"531", "104"},
	{"532", "105"},
	{"533", "106"},
	{"541", "107"},
	{"542", "108"},
	{"543", "109"},
	{"551", "110"},
	{"552", "111"},
	{"553", "112"},
	{"554", "116"},
	{"555", "117"},
	{"562", "119"},
	{"563", "120"},
	{"571", "121"},
	{"573", "122"},
	{"581", "123"},
	{"591", "124"},
	{"593", "125"},
	{"593C", "126"},
	{"717", "127"},
	{"BATAN", "344"},
}

// Modelos para leer la respuesta cruda del upstream
type UpstreamResponse struct {
	CodigoEstado  int                             `json:"CodigoEstado"`
	MensajeEstado string                          `json:"MensajeEstado"`
	Paradas       map[string][]UpstreamParadaItem `json:"paradas"`
}

type UpstreamParadaItem struct {
	Codigo                     string `json:"Codigo"`
	Identificador              string `json:"Identificador"`
	Descripcion                string `json:"Descripcion"`
	AbreviaturaBandera         string `json:"AbreviaturaBandera"`
	AbreviaturaAmpliadaBandera string `json:"AbreviaturaAmpliadaBandera"`
	LatitudParada              string `json:"LatitudParada"`
	LongitudParada             string `json:"LongitudParada"`
}

// Modelos para el JSON consolidado final
type ParadaLineaInfo struct {
	CodigoLinea     string `json:"codigoLinea"`
	NombreLinea     string `json:"nombreLinea"`
	Bandera         string `json:"bandera"`
	BanderaAmpliada string `json:"banderaAmpliada"`
}

type ParadaConsolidada struct {
	Identificador string            `json:"identificador"`
	Codigo        string            `json:"codigo"`
	Descripcion   string            `json:"descripcion"`
	Latitud       float64           `json:"latitud"`
	Longitud      float64           `json:"longitud"`
	Lineas        []ParadaLineaInfo `json:"lineas"`
}

type FinalOutput struct {
	Metadata struct {
		FechaExtraccion    string `json:"fechaExtraccion"`
		TotalLineas        int    `json:"totalLineas"`
		TotalParadasUnicas int    `json:"totalParadasUnicas"`
	} `json:"metadata"`
	Lineas []struct {
		CodigoLinea string `json:"codigoLinea"`
		Nombre      string `json:"nombre"`
	} `json:"lineas"`
	Paradas []ParadaConsolidada `json:"paradas"`
}

func main() {
	client := &http.Client{Timeout: 55 * time.Second}
	paradasMap := make(map[string]*ParadaConsolidada)

	total := len(lineas)
	fmt.Printf(">>> Iniciando scraping de %d lineas a traves de %s...\n", total, proxyURL)

	for i, l := range lineas {
		fmt.Printf("[%d/%d] Consultando Linea %s (Cod: %s)... ", i+1, total, l.Nombre, l.Codigo)

		items, err := fetchParadas(client, l.Codigo)
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
		} else {
			count := 0
			for idParada, variantes := range items {
				for _, item := range variantes {
					lat, _ := strconv.ParseFloat(item.LatitudParada, 64)
					lng, _ := strconv.ParseFloat(item.LongitudParada, 64)

					// 1. Dar de alta la parada física si no existe en el mapa
					p, exists := paradasMap[idParada]
					if !exists {
						p = &ParadaConsolidada{
							Identificador: idParada,
							Codigo:        item.Codigo,
							Descripcion:   item.Descripcion,
							Latitud:       lat,
							Longitud:      lng,
							Lineas:        make([]ParadaLineaInfo, 0),
						}
						paradasMap[idParada] = p
					}

					// 2. Asociar la línea y bandera evitando duplicados
					yaExiste := false
					for _, lin := range p.Lineas {
						if lin.CodigoLinea == l.Codigo && lin.Bandera == item.AbreviaturaBandera {
							yaExiste = true
							break
						}
					}

					if !yaExiste {
						p.Lineas = append(p.Lineas, ParadaLineaInfo{
							CodigoLinea:     l.Codigo,
							NombreLinea:     l.Nombre,
							Bandera:         item.AbreviaturaBandera,
							BanderaAmpliada: item.AbreviaturaAmpliadaBandera,
						})
					}
					count++
				}
			}
			fmt.Printf("OK (%d paradas procesadas)\n", count)
		}

		// Timer generoso aleatorio entre 2.5 y 4.5 segundos para cuidar la IP
		jitter := time.Duration(2500+rand.Intn(2000)) * time.Millisecond
		time.Sleep(jitter)
	}

	// Consolidar array final
	paradasList := make([]ParadaConsolidada, 0, len(paradasMap))
	for _, v := range paradasMap {
		paradasList = append(paradasList, *v)
	}

	out := FinalOutput{
		Paradas: paradasList,
	}
	out.Metadata.FechaExtraccion = time.Now().Format(time.RFC3339)
	out.Metadata.TotalLineas = total
	out.Metadata.TotalParadasUnicas = len(paradasList)

	for _, l := range lineas {
		out.Lineas = append(out.Lineas, struct {
			CodigoLinea string `json:"codigoLinea"`
			Nombre      string `json:"nombre"`
		}{
			CodigoLinea: l.Codigo,
			Nombre:      l.Nombre,
		})
	}

	// Guardar a disco
	jsonData, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Printf("Error serializando JSON: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(outputFile, jsonData, 0644)
	if err != nil {
		fmt.Printf("Error guardando archivo: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n>>> Completado con exito. Archivo generado: %s\n", outputFile)
	fmt.Printf(">>> Total de paradas fisicas unificadas: %d\n", len(paradasList))
}

func fetchParadas(client *http.Client, codLinea string) (map[string][]UpstreamParadaItem, error) {
	var lastErr error

	for intento := 1; intento <= 3; intento++ {
		form := url.Values{}
		form.Set("accion", "RecuperarParadasConBanderaYDestinoPorLinea")
		form.Set("codLinea", codLinea)
		form.Set("isSublinea", "0")

		req, err := http.NewRequest(http.MethodPost, proxyURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Origin", "http://localhost:8400")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(4 * time.Second)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if err != nil {
			lastErr = err
			time.Sleep(4 * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			time.Sleep(4 * time.Second)
			continue
		}

		var upResp UpstreamResponse
		if err := json.Unmarshal(bodyBytes, &upResp); err != nil {
			lastErr = fmt.Errorf("error parseando JSON: %w (muestra: %s)", err, truncate(string(bodyBytes), 100))
			time.Sleep(4 * time.Second)
			continue
		}

		return upResp.Paradas, nil
	}

	return nil, lastErr
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
