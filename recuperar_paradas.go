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
	proxyURL = "http://localhost:8079/proxy"
	// Run this command from the Go repository; it atomically replaces the
	// bundled catalogue consumed by the backend after a complete extraction.
	outputFile = "../cuando-llega-pro-backend/src/main/resources/paradas_mgp.json"
)

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

type UpstreamResponse struct {
	CodigoEstado  int             `json:"CodigoEstado"`
	MensajeEstado string          `json:"MensajeEstado"`
	ParadasRaw    json.RawMessage `json:"paradas"`
}

type UpstreamRouteResponse struct {
	CodigoEstado  int             `json:"CodigoEstado"`
	MensajeEstado string          `json:"MensajeEstado"`
	PuntosRaw     json.RawMessage `json:"puntos"`
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

// UpstreamRoutePoint is deliberately separate from a stop: MGP publishes the
// ordered road geometry in puntos, while its stops endpoint is not ordered.
type UpstreamRoutePoint struct {
	Latitud               FlexibleFloat `json:"Latitud"`
	Longitud              FlexibleFloat `json:"Longitud"`
	AbreviaturaBanderaSMP string        `json:"AbreviaturaBanderaSMP"`
	Descripcion           string        `json:"Descripcion"`
	IsPuntoPaso           bool          `json:"IsPuntoPaso"`
}

type FlexibleFloat float64

func (f *FlexibleFloat) UnmarshalJSON(value []byte) error {
	text := strings.Trim(string(value), "\" ")
	if text == "" || text == "null" {
		return nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	*f = FlexibleFloat(parsed)
	return nil
}

type ParadaLineaInfo struct {
	CodigoLinea     string `json:"lineCode"`
	NombreLinea     string `json:"lineName"`
	Bandera         string `json:"direction"`
	BanderaAmpliada string `json:"expandedDirection"`
	StopOrder       int    `json:"stopOrder"`
}

type ParadaConsolidada struct {
	Identificador string            `json:"identifier"`
	Codigo        string            `json:"code"`
	Descripcion   string            `json:"description"`
	Latitud       float64           `json:"latitude"`
	Longitud      float64           `json:"longitude"`
	Lineas        []ParadaLineaInfo `json:"lines"`
}

type PuntoRuta struct {
	Latitud     float64 `json:"latitude"`
	Longitud    float64 `json:"longitude"`
	EsPuntoPaso bool    `json:"isPassThrough"`
}

// RutaConsolidada is the drawable source of truth. Stops remain physical
// markers and are intentionally not used to construct the line geometry.
type RutaConsolidada struct {
	ID          string      `json:"id"`
	CodigoLinea string      `json:"lineCode"`
	NombreLinea string      `json:"lineName"`
	Ramal       string      `json:"branch"`
	Descripcion string      `json:"description"`
	Puntos      []PuntoRuta `json:"points"`
}

type LineaMeta struct {
	CodigoLinea string `json:"codigoLinea"`
	Nombre      string `json:"nombre"`
}

type FinalOutput struct {
	Metadata struct {
		FechaExtraccion    string `json:"fechaExtraccion"`
		TotalLineas        int    `json:"totalLineas"`
		TotalParadasUnicas int    `json:"totalParadasUnicas"`
	} `json:"metadata"`
	Lineas  []LineaMeta         `json:"lineas"`
	Paradas []ParadaConsolidada `json:"paradas"`
	Rutas   []RutaConsolidada   `json:"routes"`
}

func main() {
	client := &http.Client{Timeout: 55 * time.Second}
	paradasMap := make(map[string]*ParadaConsolidada)
	rutas := make([]RutaConsolidada, 0)
	failedLines := make([]string, 0)

	total := len(lineas)
	fmt.Printf(">>> Iniciando scraping de %d lineas a traves de %s...\n", total, proxyURL)

	for i, l := range lineas {
		fmt.Printf("[%d/%d] Consultando Linea %s (Cod: %s)... ", i+1, total, l.Nombre, l.Codigo)

		itemsOrdered, err := fetchParadasOrdered(client, l.Codigo)
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			failedLines = append(failedLines, l.Nombre+" (paradas)")
		} else {
			count := 0
			banderaCounters := make(map[string]int)

			for _, item := range itemsOrdered {
				idParada := item.Identificador
				if idParada == "" {
					idParada = item.Codigo
				}
				if idParada == "" {
					continue
				}

				lat, _ := strconv.ParseFloat(item.LatitudParada, 64)
				lng, _ := strconv.ParseFloat(item.LongitudParada, 64)

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

				orderIndex := banderaCounters[item.AbreviaturaBandera]
				banderaCounters[item.AbreviaturaBandera] = orderIndex + 1

				yaExiste := false
				for idx, lin := range p.Lineas {
					if lin.CodigoLinea == l.Codigo && lin.Bandera == item.AbreviaturaBandera {
						p.Lineas[idx].StopOrder = orderIndex
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
						StopOrder:       orderIndex,
					})
				}
				count++
			}
			fmt.Printf("OK (%d paradas procesadas)\n", count)
		}

		rutasLinea, err := fetchRutas(client, l.Codigo, l.Nombre)
		if err != nil {
			fmt.Printf("    Recorridos: ERROR: %v\n", err)
			failedLines = append(failedLines, l.Nombre+" (recorridos)")
		} else {
			rutas = append(rutas, rutasLinea...)
			fmt.Printf("    Recorridos: OK (%d ramales)\n", len(rutasLinea))
		}

		jitter := time.Duration(2500+rand.Intn(2000)) * time.Millisecond
		time.Sleep(jitter)
	}

	if len(failedLines) > 0 {
		fmt.Printf("\n>>> Extracción incompleta; no se modificó %s. Fallaron: %s\n", outputFile, strings.Join(failedLines, ", "))
		os.Exit(1)
	}

	paradasList := make([]ParadaConsolidada, 0, len(paradasMap))
	for _, v := range paradasMap {
		paradasList = append(paradasList, *v)
	}

	out := FinalOutput{
		Paradas: paradasList,
		Rutas:   rutas,
	}
	out.Metadata.FechaExtraccion = time.Now().Format(time.RFC3339)
	out.Metadata.TotalLineas = total
	out.Metadata.TotalParadasUnicas = len(paradasList)

	for _, l := range lineas {
		out.Lineas = append(out.Lineas, LineaMeta{
			CodigoLinea: l.Codigo,
			Nombre:      l.Nombre,
		})
	}

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

func fetchParadasOrdered(client *http.Client, codLinea string) ([]UpstreamParadaItem, error) {
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
			lastErr = fmt.Errorf("error parseando JSON: %w", err)
			time.Sleep(4 * time.Second)
			continue
		}

		var items []UpstreamParadaItem

		if err := json.Unmarshal(upResp.ParadasRaw, &items); err == nil {
			return items, nil
		}

		var mapItems map[string][]UpstreamParadaItem
		if err := json.Unmarshal(upResp.ParadasRaw, &mapItems); err == nil {
			for _, list := range mapItems {
				items = append(items, list...)
			}
			return items, nil
		}

		return nil, fmt.Errorf("formato no reconocido de paradas")
	}

	return nil, lastErr
}

func fetchRutas(client *http.Client, codLinea, nombreLinea string) ([]RutaConsolidada, error) {
	form := url.Values{}
	form.Set("accion", "RecuperarRecorridoParaMapaAbrevYAmpliPorEntidadYLinea")
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
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var upstream UpstreamRouteResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return nil, fmt.Errorf("error parseando recorridos: %w", err)
	}
	var points []UpstreamRoutePoint
	if err := json.Unmarshal(upstream.PuntosRaw, &points); err != nil {
		return nil, fmt.Errorf("formato no reconocido de puntos: %w", err)
	}

	byBranch := make(map[string]*RutaConsolidada)
	branchOrder := make([]string, 0)
	for _, point := range points {
		branch := point.AbreviaturaBanderaSMP
		if branch == "" {
			branch = "Principal"
		}
		route, exists := byBranch[branch]
		if !exists {
			route = &RutaConsolidada{
				ID:          codLinea + ":" + branch,
				CodigoLinea: codLinea,
				NombreLinea: nombreLinea,
				Ramal:       branch,
				Descripcion: point.Descripcion,
				Puntos:      make([]PuntoRuta, 0),
			}
			byBranch[branch] = route
			branchOrder = append(branchOrder, branch)
		}
		route.Puntos = append(route.Puntos, PuntoRuta{
			Latitud: float64(point.Latitud), Longitud: float64(point.Longitud), EsPuntoPaso: point.IsPuntoPaso,
		})
	}

	routes := make([]RutaConsolidada, 0, len(branchOrder))
	for _, branch := range branchOrder {
		if len(byBranch[branch].Puntos) > 1 {
			routes = append(routes, *byBranch[branch])
		}
	}
	return routes, nil
}
