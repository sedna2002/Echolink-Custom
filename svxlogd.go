// svxlogd.go
package main

import (
	extension "ExtensionLinux"
	logx "LogX"
	"database/sql"
	"fmt"
	"strings"

	"encoding/json"
	"log"
	"regexp"
	"runtime"

	_ "github.com/go-sql-driver/mysql"

	"bufio"
	"flag"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	//scheme       = "http"
	//pathServer   = "/Echolink"
	//pathCommande = "/rest/commandes"
	//host         = "localhost"
	//dbPassword   = "root"
	//dbUser       = "root"
	//dbName       = "Echolink"

	SIGINT_USER = syscall.Signal(0x10000)

	Echolink           = "Echolink serveur"
	version     string = "1.1.0"
	description string = "Application pour : " + Echolink
	//portServerREST        = 6504
	//logFilePath string = "nohupX.out"

	filenameLOG = "svxlink_%s.log"
)

type Watchdog struct {
	// actif
	Actif bool `json:"actif"`
	// Nombre maximum de tentatives vides avant de considérer la connexion comme perdue
	MaxEmptyAttempts int `json:"maxEmptyAttempts"`
}

type Video struct {
	Actif   bool   `json:"actif"`
	Rtsp    string `json:"rtsp"`
	Timeout int    `json:"timeout"`
	Fps     int    `json:"fps"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

type Config struct {
	PortServerREST int    `json:"portServerREST"`
	Scheme         string `json:"scheme"`
	PathServer     string `json:"pathServer"`
	PathCommande   string `json:"pathCommande"`
	Host           string `json:"host"`
	DBUser         string `json:"dbUser"`
	DBPassword     string `json:"dbPassword"`
	DBName         string `json:"dbName"`
	DBPort         int    `json:"dbPort"`
	// Indicatif radioamateur.
	Callsign string `json:"callsign"`
	// Latitude et Longitude de la station météo
	// Utilisé pour l'envoi des données vers APRS et Weather Underground
	// Ces valeurs sont des chaînes de caractères pour éviter les problèmes de précision avec les nombres flottants
	// Elles doivent être au format "latitude,longitude" (ex: "4885.66N,235.22E" pour Paris) centieme de degré
	Latitude  string   `json:"latitude"`
	Longitude string   `json:"longitude"`
	Emails    []string `json:"emails"`

	// Watchdog
	Watchdog Watchdog `json:"watchdog"`

	// Serveur Video camera
	Video Video `json:"video"`
}

/**
 * runJournalctl starts journalctl -u <unit> -f -o cat and returns its stdout pipe
 * and the command object. The caller is responsible for stopping the command.
 */
func runJournalctl(unit string, stop <-chan struct{}) (io.ReadCloser, *exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		// Simulation pour tests sous Windows
		file_LOG.WriteStringSprintLn("Mode simulation journalctl (Windows détecté)")
		f, err := os.Open("fake_journal.log")
		if err != nil {
			return nil, nil, err
		}
		return f, &exec.Cmd{}, nil
	}

	// journalctl -u <unit> -f -o cat
	cmd := exec.Command("journalctl", "-u", unit, "-f", "-o", "short-iso")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	// monitor stop signal: if stop requested, kill the process
	go func() {
		<-stop
		_ = cmd.Process.Kill()
	}()

	return stdout, cmd, nil
}

var config *Config
var file_LOG logx.File_LOG
var err error = nil
var pathServerALL string
var _, exePathLocal string = extension.GetPathExecutable()
var dbMySql *sql.DB

var (
	outDir      = flag.String("dir", exePathLocal+string(os.PathSeparator)+"Log" /*"/var/log/svxlink"*/, "directory to write logs")
	prefix      = flag.String("prefix", filenameLOG, "log filename prefix")
	compress    = flag.Bool("compress", true, "compression des fichiers ultérieurs à la date courante")
	maxsize     = flag.Int("maxsize", 0, "taille maximale du fichier courant : 0 = pas de limite")
	keepDays    = flag.Int("keep", 30, "how many compressed backups to keep (older removed)")
	jctlUnit    = flag.String("unit", "svxlink.service", "systemd unit name for journalctl -u <unit> -f")
	restartWait = flag.Int("restart-wait", 5, "seconds to wait before restarting journalctl if it exits")
)

func main() {

	flag.Parse()

	config, err = LoadConfig(exePathLocal + string(os.PathSeparator) + "config.json")
	if err != nil {
		log.Fatalf("Erreur de lecture config.json: %v", err)
	}
	pathServerALL = config.PathServer + config.PathCommande

	file_LOG = logx.File_LOG{
		Filename:                *prefix,
		FilenamePath:            *outDir,
		SizeMax:                 int64(*maxsize), // Pas de limite
		RatioSuppressionPercent: 20,
		Cmd:                     true,
		RetentionJours:          *keepDays,
		Utc:                     false,
		Compress:                *compress,
	}

	file_LOG.WriteStringSprintLn("Démarrage de 'svxlogd' version %s", version)
	file_LOG.WriteStringSprintLn("OS                      : %s", runtime.GOOS)
	file_LOG.WriteStringSprintLn("ARCH                    : %s", runtime.GOARCH)
	file_LOG.WriteStringSprintLn("Logging svxlink unit '%s' vers le dossier '%s' avec le préfixe '%s'", *jctlUnit, strings.ReplaceAll(file_LOG.FilenamePath, "%", "%%"), file_LOG.Filename)

	file_LOG.WriteStringSprintLn("Maximum size            : %v o", 0)
	file_LOG.WriteStringSprintLn("RatioSuppressionPercent : %v %%", 20)
	file_LOG.WriteStringSprintLn("RetentionJours          : %v jour(s)", file_LOG.RetentionJours)
	file_LOG.WriteStringSprintLn("Compression             : %v", file_LOG.Compress)

	file_LOG.WriteStringSprintLn("RestartWait             : %v seconde(s)", *restartWait)
	file_LOG.WriteStringSprintLn("pathServerALL           : %v", pathServerALL)

	// signal handling for graceful shutdown
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	// stop channel to kill journalctl subprocess
	jStop := make(chan struct{})
	//defer close(jStop)

	dbMySql = openDB()
	if dbMySql == nil {
		file_LOG.Error().WriteStringSprintLn("Erreur lors de l'accés à MySQL")
		return
	}

	var versionMySql string
	err = dbMySql.QueryRow("SELECT VERSION()").Scan(&versionMySql)
	if err != nil {
		file_LOG.Error().WriteStringSprintLn("Erreur lors de la lecture de la version MySQL : %v", err)
		return
	}

	file_LOG.WriteStringSprintLn("Version MySQL : %v", versionMySql)

	re := regexp.MustCompile(`:\s+(?P<indicatif>[a-zA-Z0-9-]+)\s+is running\s+(?P<application>.*?) for (?P<plateforme>.*?)\s+on a\s+(?P<appareil>.*?),\s+with\s+(?P<OS>.*?)\s+version\s+(?P<version>[a-zA-Z.0-9]+)`)
	// loop that (re)starts journalctl and consumes lines
	/*
		Nov 11 20:56:08 raspberry-desktop2 svxlink[3180]: --- EchoLink info message received from F4AMY ---
		Nov 11 20:56:08 raspberry-desktop2 svxlink[3180]: Station F4AMY
		Nov 11 20:56:08 raspberry-desktop2 svxlink[3180]: France
		Nov 11 20:56:08 raspberry-desktop2 svxlink[3180]: F4AMY is running EchoLink for Android on a samsung SM-G988B, with Android version 13.
		Nov 11 20:56:15 raspberry-desktop2 svxlink[3180]: Tx1: Turning the transmitter OFF
		Nov 11 20:56:24 raspberry-desktop2 svxlink[3180]: F4AMY: EchoLink QSO state changed to BYE_RECEIVED
		Nov 11 20:56:24 raspberry-desktop2 svxlink[3180]: F4AMY: EchoLink QSO state changed to DISCONNECTED
		Nov 11 20:56:24 raspberry-desktop2 svxlink[3180]: SimplexLogic: Deactivating module EchoLink...
		Nov 11 20:56:24 raspberry-desktop2 svxlink[3180]: Tx1: Turning the transmitter ON
		Nov 11 20:56:32 raspberry-desktop2 svxlink[3180]: Tx1: Turning the transmitter OFF
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: Tx1: Disconnected from remote transmitter at 192.168.0.26:5210: Connection timed out
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: Spurious audio packet received from 44.31.195.220
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: SimplexLogic: Loading /usr/local/share/svxlink/events.d/globals.tcl
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: SimplexLogic: Loading /usr/local/share/svxlink/events.d/locale.tcl
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: SimplexLogic: Loading /usr/local/share/svxlink/sounds/fr_FR/events.d/locale.tcl
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: SimplexLogic: Loading /usr/local/share/svxlink/events.d/EchoLinkCommon.tcl
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: Incoming EchoLink connection from F4AMY () at 44.31.195.220
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: SimplexLogic: Activating module EchoLink...
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: F4AMY: Accepting connection. EchoLink ID is 228230...
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: F4AMY: EchoLink QSO state changed to CONNECTED
		Nov 11 20:58:26 raspberry-desktop2 svxlink[3180]: Tx1: Turning the transmitter ON
		Nov 11 20:58:27 raspberry-desktop2 svxlink[3180]: --- EchoLink info message received from F4AMY ---
		Nov 11 20:58:27 raspberry-desktop2 svxlink[3180]: Station F4AMY
		Nov 11 20:58:27 raspberry-desktop2 svxlink[3180]: France
		Nov 11 20:58:27 raspberry-desktop2 svxlink[3180]: F4AMY is running EchoLink for Android on a samsung SM-G988B, with Android version 13.
		Nov 11 20:58:41 raspberry-desktop2 svxlink[3180]: F4AMY: EchoLink QSO state changed to BYE_RECEIVED
		Nov 11 20:58:41 raspberry-desktop2 svxlink[3180]: F4AMY: EchoLink QSO state changed to DISCONNECTED
		Nov 11 20:58:41 raspberry-desktop2 svxlink[3180]: SimplexLogic: Deactivating module EchoLink...
		Nov 11 20:58:41 raspberry-desktop2 svxlink[3180]: Tx1: Turning the transmitter OFF

	*/
	go func() {
		for {
			stdout, cmd, err := runJournalctl(*jctlUnit, jStop)
			if err != nil {
				file_LOG.Error().WriteStringSprintLn("failed to start journalctl: %v (retry in %d sec)", err, *restartWait)
				time.Sleep(time.Duration(*restartWait) * time.Second)
				continue
			}

			scanner := bufio.NewScanner(stdout)
			// set a large buffer for long log lines
			const maxBuf = 1024 * 1024
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, maxBuf)

			for scanner.Scan() {
				line := scanner.Text()

				file_LOG.WriteStringLn(line)

				if re.MatchString(line) {
					matches := re.FindStringSubmatch(line)
					if len(matches) >= 6 {
						// Crée un map du nom de groupe vers sa valeur
						result := make(map[string]string)
						for i, name := range re.SubexpNames() {
							if i != 0 && name != "" { // 0 = la ligne complète
								result[name] = matches[i]
							}
						}
						insertConnexion(dbMySql, result["indicatif"], result["application"], result["plateforme"], result["appareil"], result["os"], result["version"])
					} else {
						file_LOG.WriteStringSprintLn("Format non parsable : %s", line)
					}
				}

				// If date changed (rare mid-line boundary), handle rotation/compression.
				// We'll check by local time occasionally in the flush ticker as well.
			}

			// scanner ended (journalctl exited)
			if err := scanner.Err(); err != nil {
				file_LOG.Error().WriteStringSprintLn("journalctl scanner error: %v", err)
			}

			// attempt to wait the command (gives it a chance to exit cleanly)
			if cmd != nil {
				_ = cmd.Wait()
			}

			// If stop requested by program shutdown, break loop.
			select {
			case <-jStop:
				return
			default:
			}

			// restart after short sleep
			time.Sleep(time.Duration(*restartWait) * time.Second)
			file_LOG.Error().WriteStringSprintLn("journalctl terminated, restarting...")
		}
	}()

	// main control loop: flush and handle day changes, compression, cleanup
	for s := range sigc {
		file_LOG.Error().WriteStringSprintLn("signal %v received, shutting down...", s)
		// stop journalctl subprocess by closing channel
		close(jStop)
		file_LOG.Error().WriteStringSprintLn("")
		os.Exit(0)
		//return
	}
}

// LoadConfig charge la configuration à partir d'un fichier JSON.
// Elle lit le fichier spécifié par le nom de fichier, décode le contenu JSON
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = json.Unmarshal(data, &cfg)
	return &cfg, err
}

func openDB() *sql.DB {
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/", config.DBUser, config.DBPassword, config.Host, config.DBPort))
	if err != nil || db == nil {
		log.Fatal(err)
	}

	/*
		   	CREATE TABLE connexions (
		       id INT AUTO_INCREMENT PRIMARY KEY,
		       indicatif VARCHAR(20),
		       plateforme VARCHAR(100),
		       appareil VARCHAR(100),
			   os VARCHAR(100),
		       version VARCHAR(20),
		       date_connexion DATETIME DEFAULT CURRENT_TIMESTAMP
		   );
	*/
	_, err = db.Exec(`CREATE DATABASE IF NOT EXISTS echolink;`)
	if err != nil {
		file_LOG.Fatalf("Erreur de creation de la table %v: %v", "connexions", err)
		return nil
	}

	db, err = sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", config.DBUser, config.DBPassword, config.Host, config.DBPort, config.DBName))
	if err != nil || db == nil {
		file_LOG.Fatalf("%v", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS connexions (
		id INT AUTO_INCREMENT PRIMARY KEY,
		indicatif VARCHAR(20),
		plateforme VARCHAR(100),
		appareil VARCHAR(100),
		application VARCHAR(100),
		os VARCHAR(100),
		version VARCHAR(20),
		date_connexion DATETIME DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		file_LOG.Fatalf("Erreur de creation de la table %v: %v", "connexions", err)
		return nil
	}

	return db
}

/**
 *
 */
func insertConnexion(db *sql.DB, indicatif, application, plateforme, appareil, os, version string) {
	_, err := db.Exec(`
        INSERT INTO connexions (indicatif, application, plateforme, appareil, os, version)
        VALUES (?, ?, ?, ?, ?, ?)`,
		indicatif, application, plateforme, appareil, os, version)
	if err != nil {
		file_LOG.Error().WriteStringSprintLn("Erreur insertion: %v", err)
	}
}
