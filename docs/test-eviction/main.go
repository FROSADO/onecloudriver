//go:build ignore

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/frosado/onecloudriver/internal/fs"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Logs detallados para ver la actividad del controlador
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	log.Info().Msg("=== Iniciando prueba de EvictionController ===")

	// Contador de ejecuciones del callback
	sweepCount := 0

	// Crear controlador con intervalo de 3 segundos
	ctrl := fs.NewEvictionController(3*time.Second, func() {
		sweepCount++
		log.Info().Int("sweep", sweepCount).Msg("Callback ejecutado (sweep)")
	})

	// Iniciar el goroutine periódico
	ctrl.Start()
	log.Info().Msg("Controlador iniciado, esperando ticks...")

	// Esperar 12 segundos para que se ejecuten varios sweeps periódicos
	time.Sleep(12 * time.Second)

	// Llamar a RunOnce manualmente (debería ejecutarse y no solaparse con el periódico)
	log.Info().Msg("Llamando a RunOnce manual...")
	ctrl.RunOnce(func() {
		log.Info().Msg("Ejecución manual (RunOnce)")
	})

	// Esperar un poco para que la ejecución manual se complete
	time.Sleep(2 * time.Second)

	// Llamar a RunSerialized para ver la serialización
	log.Info().Msg("Llamando a RunSerialized...")
	ctrl.RunSerialized(func() {
		log.Info().Msg("Ejecución serializada (RunSerialized)")
		time.Sleep(2 * time.Second) // simular trabajo largo
	})

	// Detener el controlador
	log.Info().Msg("Deteniendo controlador...")
	ctrl.Stop()

	log.Info().Msg("=== Prueba completada ===")
}
