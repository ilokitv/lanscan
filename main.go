package main

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/xuri/excelize/v2"
)

type ScanResult struct {
	IP       string
	MAC      string
	Hostname string
	Ports    []int
	Alive    bool
}

// Получение имени устройства по IP
func getHostname(ip string) string {
	names, err := net.LookupAddr(ip)
	if err != nil || len(names) == 0 {
		return "Неизвестно"
	}
	return strings.TrimSuffix(names[0], ".")
}

// Получение MAC-адреса по IP
func getMACAddress(ip string) string {
	var cmd *exec.Cmd
	var macRegex *regexp.Regexp

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("arp", "-a", ip)
		macRegex = regexp.MustCompile(`([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})`)
	default: // Linux и MacOS
		cmd = exec.Command("arp", "-n", ip)
		macRegex = regexp.MustCompile(`([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}`)
	}

	output, err := cmd.Output()
	if err != nil {
		return "Неизвестно"
	}

	matches := macRegex.FindString(string(output))
	if matches == "" {
		return "Неизвестно"
	}
	return strings.ToUpper(matches)
}

// Экспорт результатов в Excel
func exportToExcel(results []ScanResult) error {
	f := excelize.NewFile()
	defer f.Close()

	sheetName := "Результаты сканирования"
	f.SetSheetName("Sheet1", sheetName)

	// Обновляем заголовки
	headers := []string{"IP-адрес", "MAC-адрес", "Имя устройства", "Открытые порты"}
	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue(sheetName, cell, header)
	}

	// Заполняем данные
	for i, result := range results {
		row := i + 2
		ports := ""
		if len(result.Ports) > 0 {
			portsStr := make([]string, len(result.Ports))
			for j, port := range result.Ports {
				portsStr[j] = fmt.Sprintf("%d", port)
			}
			ports = strings.Join(portsStr, ", ")
		}

		f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), result.IP)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), result.MAC)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), result.Hostname)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), ports)
	}

	// Автоматическая ширина столбцов
	for i := 0; i < 4; i++ {
		col := string('A' + i)
		f.SetColWidth(sheetName, col, col, 20)
	}

	return f.SaveAs("network_scan_results.xlsx")
}

// Проверка доступности хоста через ping
func isHostAlive(ip string) bool {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("ping", "-n", "1", "-w", "500", ip)
	default: // Linux и MacOS
		cmd = exec.Command("ping", "-c", "1", "-W", "1", ip)
	}

	err := cmd.Run()
	return err == nil
}

func scanPort(host string, port int, wg *sync.WaitGroup, results chan<- int) {
	defer wg.Done()
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 300*time.Millisecond)

	if err != nil {
		return
	}
	defer conn.Close()
	results <- port
}

func scanHost(ip string) []int {
	var wg sync.WaitGroup
	results := make(chan int, 1024)
	openPorts := []int{}

	// Список наиболее распространенных портов
	commonPorts := []int{20, 21, 22, 23, 25, 53, 80, 110, 111, 135, 139, 143, 443, 445, 993, 995, 1723, 3306, 3389, 5900, 8080}

	for _, port := range commonPorts {
		wg.Add(1)
		go scanPort(ip, port, &wg, results)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for port := range results {
		openPorts = append(openPorts, port)
	}

	return openPorts
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func main() {
	myApp := app.New()
	window := myApp.NewWindow("LanScan v1.0")

	// Создаем элементы интерфейса
	progress := widget.NewProgressBar()
	deviceCount := widget.NewLabel("Найдено устройств: 0")
	deviceCount.TextStyle = fyne.TextStyle{Bold: true}

	var listLength int

	// Создаем список для отображения результатов
	list := widget.NewList(
		func() int { return listLength },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel(""), // IP
				widget.NewLabel(""), // MAC
				widget.NewLabel(""), // Hostname
				widget.NewLabel(""), // Ports
			)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			// Будет заполнено позже
		},
	)

	listScroll := container.NewScroll(list)
	listScroll.SetMinSize(fyne.NewSize(600, 300))

	var scanResults []ScanResult

	scanBtn := widget.NewButton("Начать сканирование", nil)
	exportBtn := widget.NewButton("Экспорт в Excel", nil)
	exportBtn.Disable()

	scanBtn.OnTapped = func() {
		progress.SetValue(0)
		scanBtn.Disable()
		exportBtn.Disable()
		scanResults = nil
		deviceCount.SetText("Найдено устройств: 0")

		listLength = 0
		list.Refresh()

		go func() {
			localIP := getLocalIP()
			if localIP == "" {
				dialog.ShowError(fmt.Errorf("не удалось определить локальный IP-адрес"), window)
				scanBtn.Enable()
				return
			}

			ipParts := net.ParseIP(localIP).To4()
			if ipParts == nil {
				dialog.ShowError(fmt.Errorf("неверный формат IP-адреса"), window)
				scanBtn.Enable()
				return
			}

			baseIP := fmt.Sprintf("%d.%d.%d.", ipParts[0], ipParts[1], ipParts[2])

			var wg sync.WaitGroup
			results := make(chan ScanResult, 255)

			for i := 1; i < 255; i++ {
				ip := fmt.Sprintf("%s%d", baseIP, i)
				wg.Add(1)
				go func(ip string) {
					defer wg.Done()

					if isHostAlive(ip) {
						hostname := getHostname(ip)
						mac := getMACAddress(ip)
						ports := scanHost(ip)
						results <- ScanResult{
							IP:       ip,
							MAC:      mac,
							Hostname: hostname,
							Ports:    ports,
							Alive:    true,
						}
					}
					progress.SetValue(progress.Value + 1.0/254.0)
				}(ip)
			}

			go func() {
				wg.Wait()
				close(results)
			}()

			for result := range results {
				if result.Alive {
					scanResults = append(scanResults, result)
					deviceCount.SetText(fmt.Sprintf("Найдено устройств: %d", len(scanResults)))
				}
			}

			listLength = len(scanResults)
			list.UpdateItem = func(id widget.ListItemID, item fyne.CanvasObject) {
				result := scanResults[id]
				container := item.(*fyne.Container)

				ipLabel := container.Objects[0].(*widget.Label)
				ipLabel.SetText(result.IP)

				macLabel := container.Objects[1].(*widget.Label)
				macLabel.SetText(result.MAC)

				hostnameLabel := container.Objects[2].(*widget.Label)
				hostnameLabel.SetText(result.Hostname)

				portsLabel := container.Objects[3].(*widget.Label)
				if len(result.Ports) > 0 {
					portsStr := make([]string, len(result.Ports))
					for i, port := range result.Ports {
						portsStr[i] = fmt.Sprintf("%d", port)
					}
					portsLabel.SetText(strings.Join(portsStr, ", "))
				} else {
					portsLabel.SetText("Нет открытых портов")
				}
			}
			list.Refresh()

			progress.SetValue(1.0)
			scanBtn.Enable()
			if len(scanResults) > 0 {
				exportBtn.Enable()
			}
		}()
	}

	exportBtn.OnTapped = func() {
		if err := exportToExcel(scanResults); err != nil {
			dialog.ShowError(err, window)
		} else {
			dialog.ShowInformation("Успех", "Результаты сохранены в файл network_scan_results.xlsx", window)
		}
	}

	// Заголовки колонок
	headers := container.NewHBox(
		widget.NewLabelWithStyle("IP-адрес", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("MAC-адрес", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Имя устройства", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("Открытые порты", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)

	headerLabel := widget.NewLabelWithStyle(
		"Сканер локальной сети",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	buttonBox := container.NewHBox(scanBtn, exportBtn)
	controls := container.NewVBox(
		widget.NewLabel("Нажмите кнопку для начала сканирования"),
		buttonBox,
		progress,
		deviceCount,
	)

	// Создаем ссылку на GitHub
	githubURL, _ := url.Parse("https://github.com/ilokitv")
	githubLink := widget.NewHyperlink("GitHub: @ilokitv", githubURL)

	// Создаем контейнер для подвала с выравниванием по центру
	footer := container.NewCenter(githubLink)

	// Обновляем основной контейнер, добавляя разделитель и подвал
	content := container.NewVBox(
		headerLabel,
		widget.NewSeparator(),
		controls,
		widget.NewSeparator(),
		headers,
		container.NewPadded(listScroll),
		widget.NewSeparator(),
		footer,
	)

	window.Resize(fyne.NewSize(800, 600))
	window.SetContent(content)
	window.CenterOnScreen()
	window.ShowAndRun()
}
