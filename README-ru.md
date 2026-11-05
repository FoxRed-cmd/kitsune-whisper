<p align="center">
  <b>Читать на:</b>
  <a href="README.md">English</a> ·
  <b>Русский</b>
</p>

<div align="center">
  <img src="client/internal/brand/icons/app.svg" width="112" alt="Иконка kitsune-whisper">
  <h1>kitsune-whisper</h1>
  <p>
    <strong>Приватная диктовка в текст по локальной сети для Linux и Windows.</strong><br>
    Нажмите горячую клавишу, говорите, нажмите ещё раз — текст появляется у курсора.<br>
    Аудио не покидает вашу сеть.
  </p>

  [![Release](https://img.shields.io/github/v/release/FoxRed-cmd/kitsune-whisper?style=flat-square&label=release&color=4d6bf5)](https://github.com/FoxRed-cmd/kitsune-whisper/releases/latest)
  [![License](https://img.shields.io/badge/license-MIT-4d6bf5?style=flat-square)](LICENSE)
  [![Platform](https://img.shields.io/badge/platform-Linux%20%7C%20Windows-4d6bf5?style=flat-square)](#быстрый-старт)
  [![Privacy](https://img.shields.io/badge/privacy-LAN--only-2ea043?style=flat-square)](#приватность)
</div>

---

**Быстрый старт:** [Linux](#linux) · [Windows](#windows) · [CUDA](#cuda-опционально) · [Модели](#модели) · [Решение проблем](#решение-проблем)

## Что это

kitsune-whisper превращает речь в текст в любом приложении. Небольшой Клиент
работает в вашей сессии рабочего стола, записывает с микрофона и вставляет
расшифровку туда, где находится курсор. Сервер распознаёт речь с помощью
[faster-whisper](https://github.com/SYSTRAN/faster-whisper) на процессоре или
видеокарте NVIDIA.

- **Глобальная горячая клавиша** — запускайте и останавливайте диктовку, не
  покидая приложение.
- **Локально по умолчанию** — аудио уходит на Сервер на вашей машине или в
  вашей локальной сети, а не в облако.
- **Нативная установка** — Сервер поставляется со своим Python. Не нужны Python,
  pip, виртуальное окружение или Docker.
- **CUDA, когда доступна** — Сервер сам определяет видеокарту NVIDIA и
  переключается на процессор, если её нет.
- **Опциональная локальная обработка** — Refine и Summarize могут очистить или
  сократить расшифровку с помощью локальной LLM.

> [!NOTE]
> **Один конфиг, два процесса.** Клиент и Сервер читают каждый свой раздел
> одного `kitsune.yaml`. Установка обеих частей на одном компьютере — вариант по
> умолчанию, сопряжение не требуется: Клиент сам запускает локальный Сервер.

## Быстрый старт

Для каждой платформы есть один установщик. Он кладёт файлы в ваш профиль, не
требует прав администратора и ничего не запускает. Повторный запуск той же
команды обновляет установку на месте; конфигурация, модели и сопряжение
сохраняются.

## Зависимости

Зависимости различаются по платформам. Они перечислены в шагах для
[Linux](#linux) и [Windows](#windows) ниже. Установщик не устанавливает системные
пакеты — поставьте их сами до или после него.

### Linux

**Требования:** 64-битный Linux с **glibc 2.39 или новее** для Клиента —
Ubuntu 24.04+, Debian 13+, Fedora 43+, Arch или openSUSE Tumbleweed. Сам
Сервер работает на glibc 2.35+ (Ubuntu 22.04, Debian 12, openSUSE Leap 15.6);
на таких хостах устанавливайте с `--server-only`.

#### 1. Установите системные библиотеки

| Дистрибутив | Команда |
| --- | --- |
| Debian / Ubuntu | `sudo apt update && sudo apt install -y libgtk-4-1 libwebkitgtk-6.0-4 libasound2 libx11-6` |
| Fedora | `sudo dnf install -y gtk4 webkitgtk6.0 alsa-lib libX11` |
| Arch | `sudo pacman -S --needed gtk4 webkitgtk-6.0 alsa-lib libx11` |
| openSUSE | `sudo zypper install -y libgtk-4-1 libwebkitgtk-6_0-4 libasound2 libX11-6` |

- **GTK4 + WebKitGTK 6.0** — встроенное окно настроек. Без них настройки
  откроются в браузере.
- **ALSA** — запись с микрофона.
- **X11** — глобальные горячие клавиши и вставка текста. Библиотека нужна даже в
  сеансе Wayland, потому что Клиент может переключиться на X11 через XWayland.

#### 2. Wayland: установите инструмент вставки

Сначала проверьте тип сеанса:

```sh
echo "$XDG_SESSION_TYPE"   # x11 или wayland
```

> [!TIP]
> **Рекомендация: X11 — самый простой путь.** Ему не нужен дополнительный
> помощник, и он полностью поддерживает глобальные горячие клавиши. Wayland тоже
> работает, но вставка текста в нём best-effort и требует один из инструментов
> ниже.

| Сеанс | Что установить | Что работает |
| --- | --- | --- |
| **X11** | ничего дополнительно | Горячие клавиши и вставка встроены. |
| **Wayland, wlroots** — Sway, Hyprland, river, … | `wtype` + `wl-clipboard` | Предпочтителен `wtype`; настройка `/dev/uinput` не нужна. |
| **Wayland, GNOME (Mutter)** | `ydotool` + `wl-clipboard` | `wtype` не работает. Портал в GNOME 48+, иначе своя комбинация. |
| **Wayland, KDE Plasma** | `ydotool` + `wl-clipboard` | `wtype` не используется. Портал в Plasma 5.27+, иначе добавьте команду вручную. |
| **Wayland, без помощника** | — | Только **буфер обмена**: текст копируется, нажмите `Ctrl+V` сами. |

Выберите столбец, который соответствует вашему сеансу:

| Дистрибутив | wlroots — `wtype` | GNOME/KDE — `ydotool` | Только буфер обмена |
| --- | --- | --- | --- |
| Debian / Ubuntu | `sudo apt install -y wl-clipboard wtype` | `sudo apt install -y wl-clipboard ydotool` | `sudo apt install -y wl-clipboard` |
| Fedora | `sudo dnf install -y wl-clipboard wtype` | `sudo dnf install -y wl-clipboard ydotool` | `sudo dnf install -y wl-clipboard` |
| Arch | `sudo pacman -S --needed wl-clipboard wtype` | `sudo pacman -S --needed wl-clipboard ydotool` | `sudo pacman -S --needed wl-clipboard` |
| openSUSE | `sudo zypper install -y wl-clipboard wtype` | `sudo zypper install -y wl-clipboard ydotool` | `sudo zypper install -y wl-clipboard` |

На wlroots основной вариант — `wtype`. Если он не работает, выполните команду из
столбца GNOME/KDE для своего дистрибутива, чтобы добавить `ydotool` как запасной;
оба инструмента могут стоять одновременно. В GNOME/KDE `wtype` не работает,
поэтому устанавливайте только столбец с `ydotool`.

> [!NOTE]
> **Особенности дистрибутивов.** В Debian 13 `ydotool` лежит в
> `trixie-backports`, а не в основном наборе. В openSUSE Leap 15.6 нет
> `ydotool`; используйте `wtype` на wlroots-композиторе или соберите `ydotool`
> из upstream для GNOME/KDE.

#### 3. Настройте `ydotool` (Wayland GNOME/KDE)

Пропустите этот шаг на X11. На wlroots `wtype` не требует настройки; ставьте
`ydotool` только как запасной вариант. В GNOME, KDE Plasma и любом сеансе, где
`wtype` не работает, для `ydotool` нужны две вещи: доступ к `/dev/uinput` и
демон `ydotoold`.

**a. Дайте доступ к `/dev/uinput`.** Рекомендуемое правило `uaccess` действует
сразу и не требует повторного входа:

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput

# Должна появиться строка с вашим именем пользователя:
getfacl /dev/uinput | grep "user:$USER"
```

<details>
<summary><b>Альтернатива: группа <code>input</code></b></summary>

Даёт группе `input` право записи. Чтобы новое членство в группе применилось,
нужно выйти и войти снова.

```sh
sudo tee /etc/udev/rules.d/70-kitsune-whisper-uinput.rules >/dev/null <<'EOF'
KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660"
EOF
sudo modprobe uinput
sudo udevadm control --reload-rules
sudo udevadm trigger --name-match=uinput
sudo usermod -aG input "$USER"

# После выхода и повторного входа:
id -nG | grep -w input
```

</details>

**b. Запустите `ydotoold` в своей сессии.** Используйте systemd-сервис
**пользователя** и запускайте ровно один демон.

Создайте юнит:

```ini
# ~/.config/systemd/user/ydotoold.service
[Unit]
Description=ydotool input daemon
PartOf=graphical-session.target
After=graphical-session.target

[Service]
ExecStart=/usr/bin/ydotoold
Restart=on-failure

[Install]
WantedBy=graphical-session.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now ydotoold.service
systemctl --user status ydotoold.service --no-pager
```

Если `command -v ydotoold` показывает другой путь, укажите его в `ExecStart`.

> [!WARNING]
> **Не запускайте `ydotoold &` и сервис одновременно.** Они используют один и
> тот же сокет, и второй демон завершается с ошибкой
> `Another ydotoold is running with the same socket`. Если это уже случилось,
> остановите ручной демон и перезапустите сервис:
>
> ```sh
> pkill -x ydotoold
> systemctl --user reset-failed ydotoold.service
> systemctl --user start ydotoold.service
> ```

Для разовой проверки без сервиса достаточно `ydotoold &`. Перед включением
сервиса остановите его командой `pkill -x ydotoold`.

**c. Проверьте вставку.** Сфокусируйтесь на текстовом поле и выполните:

```sh
ydotool type "hello from ydotool"
ydotool key 29:1 47:1 47:0 29:0    # Ctrl+V
```

Если демон слушает нестандартный сокет, задайте `YDOTOOL_SOCKET` в окружении
Клиента. По умолчанию используется `$XDG_RUNTIME_DIR/.ydotool_socket`.

**d. Выберите помощника.** По умолчанию `client.wayland_tool: auto` сначала
пробует `wtype`, затем `ydotool`. В GNOME/KDE `wtype` недоступен, поэтому
`ydotool` используется автоматически. Чтобы задать его явно, укажите в
`kitsune.yaml` и перезапустите Клиент:

```yaml
client:
  wayland_tool: ydotool   # auto | wtype | ydotool | none
```

Перезапустите Клиент после установки помощника или изменения этого значения,
чтобы он заново определил доступные инструменты.

> [!WARNING]
> **Если `ydotoold` не запущен, вставка не работает.** Клиент не поставляет
> `ydotoold` и не управляет им; он переходит в режим только буфера обмена и
> пишет в лог подсказку «нажмите Ctrl+V». Проверьте
> `systemctl --user status ydotoold` и ACL `/dev/uinput`, прежде чем винить
> Клиент.

> [!TIP]
> **Горячие клавиши в Wayland.** В GNOME 48+ и KDE Plasma 5.27+ Клиент
> использует портал `GlobalShortcuts`; подтвердите запрос один раз. В Sway,
> Hyprland или на старом рабочем столе привяжите `kitsune-client toggle` и
> `kitsune-client cancel` в конфигурации композитора. Окно настроек умеет
> добавлять сочетания в GNOME.

#### 4. Установите kitsune-whisper

```sh
# Клиент + локальный Сервер (рекомендуется для одного компьютера)
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

```sh
# Только Клиент — Сервер работает на другой машине
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --client-only

# Только Сервер — выделенный хост без рабочего стола
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --server-only

# Зафиксировать версию
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --version v1.0.0
```

Проверьте план или установленную версию, ничего не меняя:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --check
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --dry-run
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --help
```

#### 5. Запустите и завершите мастер настройки

Откройте **kitsune-whisper** в меню приложений или выполните:

```sh
kitsune-client gui
```

Мастер первого запуска настроит микрофон, горячую клавишу, оборудование и
модель. Загрузка модели показывает прогресс, её можно отложить. **Во время
установки ничего не скачивается**, и Сервер ничего не скачивает при запуске.

Автозапуск по умолчанию выключен. Включите его в **Настройки → Автозапуск**,
если хотите, чтобы Клиент и локальный Сервер запускались при входе.

### Windows

**Требования:** 64-битная Windows 10 сборки **17763 (1809)** или новее,
**PowerShell 7+** и **Microsoft Edge WebView2 Runtime**.

1. **Установите PowerShell 7**, если его нет:

   ```powershell
   winget install --id Microsoft.PowerShell --source winget
   ```

   Или воспользуйтесь официальным установщиком:
   <https://aka.ms/powershell-release?tag=stable>.

2. **Установите WebView2** — он нужен для настроек и трея. Windows Update часто
   ставит его сам; иначе установите Evergreen Runtime с
   <https://developer.microsoft.com/microsoft-edge/webview2/>.

3. **Установите kitsune-whisper** в PowerShell 7:

   ```powershell
   irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
   ```

   С флагами оберните скачанный скрипт:

   ```powershell
   # Только Клиент
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -ClientOnly

   # Только Сервер
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -ServerOnly

   # Проверить установленную и доступную версии
   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Check
   ```

4. **Запустите** **kitsune-whisper** из меню «Пуск» и пройдите тот же мастер
   первого запуска.

> [!TIP]
> **Установили только Клиент?** Мастер попросит адрес Сервера
> (`https://<server>:8765`) и одноразовый код подключения. На хосте с Сервером
> выполните `kitsune-server pair` (или
> `docker exec <container> kitsune-server pair` для Docker) и вставьте код в
> **Настройки → Серверы и устройства**.

> [!WARNING]
> **Сборка без подписи.** SmartScreen может показать *Windows protected your PC*
> при первом запуске. Выбирайте **More info → Run anyway** только если скачали
> установщик из этого репозитория. Скачанному `install.ps1` может понадобиться
> `Unblock-File .\install.ps1`.

> [!NOTE]
> **Дополнительно о Windows.** Visual C++ Redistributable не нужен, задача в
> планировщике при входе не запускается. Установщик никогда не скачивает
> WebView2, CUDA или другие системные компоненты.

### CUDA (опционально)

Ни один установщик не скачивает CUDA. `server.device: auto` использует
видеокарту, если есть рабочий драйвер NVIDIA и cuBLAS, иначе переключается на
процессор. Дополнительное окружение занимает около **550 МБ** и ставится в
комплект Сервера, а не в системный Python.

**Сначала драйвер.** Установите проприетарный драйвер NVIDIA средствами
дистрибутива или с <https://www.nvidia.com/drivers/>, перезагрузитесь и
убедитесь, что `nvidia-smi` работает. Без него Сервер не сможет использовать GPU.

**Linux** — после того как драйвер заработал:

```sh
"$HOME/.local/lib/kitsune-whisper/server/runtime/bin/python3" -m pip install \
  --target "$HOME/.local/lib/kitsune-whisper/server/lib" nvidia-cublas-cu12
```

**Windows** — после того как драйвер заработал:

```powershell
& "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\runtime\python.exe" -m pip install `
  --target "$env:LOCALAPPDATA\Programs\kitsune-whisper\Server\lib" nvidia-cublas-cu12
```

После этого перезапустите Сервер (**Трей → Перезапустить клиент** или
перезапустите Клиент). Шаг «Оборудование» в мастере и аутентифицированный
`GET /status` показывают выбранное устройство (`device`: `cuda` или `cpu`).

> [!IMPORTANT]
> **После обновления на Linux повторите команду CUDA.** Установщик Linux
> заменяет комплект Сервера, поэтому cuBLAS нужно поставить заново, если
> `/status` снова показывает CPU. Установщик Windows сохраняет
> `Server\lib\nvidia` при обновлении.

> [!TIP]
> **Мало видеопамяти?** Укажите `server.compute_type: int8_float16` в
> `kitsune.yaml`, чтобы разместить модель побольше на небольшой видеокарте. При
> `auto` Сервер выбирает `float16` на CUDA и `int8` на процессоре.

## Docker (отдельный хост Сервера)

Контейнер нужен, когда Сервер должен работать на другой машине. Внутри тот же
код Сервера, API, TLS, токены устройств и каталог моделей, что и в нативном
bundle.

```sh
mkdir -p config
cp kitsune.example.yaml config/kitsune.yaml
docker compose --profile cpu up -d    # или: --profile gpu
docker exec <container> kitsune-server pair
```

- `./config/kitsune.yaml` смонтирован на запись; образ задаёт
  `KITSUNE_CONFIG=/config/kitsune.yaml`. Редактируйте файл на хосте или дайте
  Admin API сохранить настройки.
- Том `kitsune-state` хранит сертификат, токены устройств, флаг мягкой остановки
  и журнал, поэтому отпечаток сертификата переживает пересоздание контейнера.
- Том `kitsune-models` хранит скачанные модели между обновлениями.
- `docker compose down` сохраняет оба тома. `docker compose down -v` удаляет их
  вместе с сертификатом, всеми устройствами и моделями.

Полные команды, сопряжение и удаление: [install.md#docker](docs/install.md#docker).

## Модели

Сервер загружает **одну модель Whisper за раз**. Модели не входят в установщик:
мастер первого запуска скачивает выбранную с прогрессом.

| Модель | Диск | Память | Скорость | Качество | Для чего |
| --- | --- | --- | --- | --- | --- |
| `tiny` | ~75 МБ | ~450 МБ | быстро | базовое | первая проверка, очень маленькие хосты |
| `base` | ~145 МБ | ~600 МБ | быстро | хорошее | диктовка на процессоре |
| `small` | ~484 МБ | ~1.3 ГБ | средне | хорошее | сбалансированный вариант по умолчанию; процессор или небольшая GPU |
| `medium` | ~1.5 ГБ | ~3.0 ГБ | медленно | лучшее | подходящая GPU NVIDIA |
| `large-v3` | ~3.1 ГБ | ~5.2 ГБ | медленно | лучшее | GPU с большим объёмом памяти |

**Какую выбрать?** Мастер рекомендует **`base` на процессоре** и **`medium` на
CUDA**. Если не уверены, начните с `small`; на хосте только с процессором `base`
примерно в 2–3 раза быстрее `small` при небольшой потере качества.

> [!TIP]
> **Первая фраза после простоя медленнее.** По умолчанию модель выгружается
> после 10 минут без запросов. Измените это через `server.idle_unload_seconds`;
> `0` держит модель в памяти постоянно.

> [!NOTE]
> **Где лежат модели.** Linux: `~/.cache/kitsune-whisper/models`. Windows:
> `%USERPROFILE%\.cache\kitsune-whisper\models`. Docker: том `kitsune-models`.
> `server.download_root` меняет каталог. Модель, уже находящаяся в кеше Hugging
> Face, переиспользуется и не скачивается повторно.

## Диктовка

| Действие | По умолчанию |
| --- | --- |
| Начать / остановить запись | `Ctrl+Shift+Space` |
| Отменить без вставки | `Ctrl+Shift+Q` |

Оба сочетания настраиваются в **Настройки → Распознавание речи**. Расшифровка
вставляется в активное приложение. Пустая расшифровка ничего не вставляет.

> [!TIP]
> **Ничего не теряется, если Сервер недоступен.** Неудачная запись сохраняется в
> Spool (сохранённые записи), а не выбрасывается. Если вставка в Wayland
> недоступна, текст всё равно копируется в буфер обмена.

## Обновление, проверка и удаление

Повторный запуск установщика обновляет на месте и сохраняет `kitsune.yaml`,
модели, сертификаты, устройства и сопряжение.

**Linux**

```sh
# Проверить установленную и доступную версии
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --check

# Обновить
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash

# Удалить приложение, сохранив конфигурацию, модели и сопряжение
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --uninstall

# Удалить все следы, созданные установщиком
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash -s -- --uninstall --purge
```

**Windows**

```powershell
# Проверить установленную и доступную версии
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Check

# Обновить
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex

# Удалить приложение, сохранив конфигурацию, модели и сопряжение
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall

# Удалить все следы, созданные установщиком
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall -Purge
```

Ни одна команда удаления не удаляет пакеты, которые вы установили сами, или
udev-правило для `ydotool`; это нужно делать вручную.

## Решение проблем

| Симптом | Решение |
| --- | --- |
| Настройки открываются в браузере, а не в окне | Установите **GTK4** и **WebKitGTK 6.0**. |
| Клиент не может записывать | Установите **ALSA** (`libasound2` / `alsa-lib`). |
| Горячая клавиша не работает в Wayland | Подтвердите запрос портала или привяжите `kitsune-client toggle` в композиторе. |
| Текст появляется только в буфере обмена | Установите `wtype` (wlroots) или `ydotool` (GNOME/KDE); убедитесь, что `ydotoold` запущен. |
| `Another ydotoold is running with the same socket` | Остановите ручной демон и перезапустите сервис: `pkill -x ydotoold && systemctl --user reset-failed ydotoold.service && systemctl --user start ydotoold.service`. |
| `ydotool: failed to open /dev/uinput` | Повторно примените udev-правило, выполните `udevadm trigger` и проверьте `getfacl /dev/uinput`. |
| GPU не используется | Проверьте `nvidia-smi`, установите `nvidia-cublas-cu12`, перезапустите Сервер и проверьте `/status`. |
| `503 model_unavailable` | Модель ещё не установлена. Откройте **Настройки → Модели** и скачайте её. |
| Сервер недоступен | Проверьте, что Клиент и Сервер используют один адрес, и что устройство всё ещё сопряжено. |

Логи:

- **Клиент Linux:** `~/.cache/kitsune-whisper/client.log`
- **Сервер Linux:** `~/.local/state/kitsune-whisper/server.log`
- **Windows:** `%LOCALAPPDATA%\kitsune-whisper\client.log` и
  `%LOCALAPPDATA%\kitsune-whisper\server.log`

Подробнее: [docs/troubleshooting.md](docs/troubleshooting.md).

## Документация

Подробные страницы документации пока на английском.

- [Установка, флаги, пути, автозапуск, обновление](docs/install.md)
- [Полная матрица зависимостей и настройка вставки в Wayland](docs/dependencies.md)
- [Справочник по конфигурации](docs/configuration.md)
- [Безопасность: TLS, токены устройств, сопряжение](docs/security.md)
- [Модели: каталог, загрузка, активация, удаление](docs/models.md)
- [Решение проблем](docs/troubleshooting.md)
- [Среда разработки и команды](docs/development.md)
- [Процесс релиза и артефакты](docs/release.md)
- [Матрица ручной проверки](docs/verification/manual-matrix.md)
- [Поддержка Wayland и ограничения](docs/wayland.md)
- [Архитектурные решения](docs/adr/) и [глоссарий](GLOSSARY.md)

## Приватность

Аудио отправляется только от Клиента на Сервер, который вы настроили, по вашей
локальной сети. Сервер никуда не отправляет аудио. Опциональные
Refine/Summarize **по умолчанию выключены**; если указать в
`server.processing.base_url` облачного провайдера, на хост уходит **текст**
расшифровки, но аудио по-прежнему остаётся в вашей сети.

## Лицензия

Проект распространяется по [лицензии MIT](LICENSE).
