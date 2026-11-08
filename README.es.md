<div align="center">

<img src="docs/media/logo.svg" alt="Seelegent" width="344">

**Un agente principal planifica y revisa. Las CLI de programación de tus propias cuentas escriben el código.**

[Instalación](#instalación) · [Inicio rápido](#inicio-rápido) · [Guía de uso](docs/usage.md) ·
[Documentación](docs/README.md) · [Contribuir](CONTRIBUTING.md)

[English](README.md) · [Tiếng Việt](README.vi.md) · [简体中文](README.zh-CN.md) · **Español** ·
[한국어](README.ko.md) · [日本語](README.ja.md) · [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#requisitos)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<p align="center">
  <a href="docs/media/hero.mp4"><img src="docs/media/hero.webp" width="900"
    alt="El agente lead delega una tarea con Seelegent; el panel muestra los pasos del worker, el gate y el diff"></a>
</p>

Seelegent permite que un agente de programación principal (el lead) reparta trabajo entre workers: CLI de programación
que se ejecutan con tus propias cuentas. Tú hablas con el lead (Claude Code, Codex, Antigravity, OpenCode u otro host
MCP); el lead diseña el trabajo, escribe un brief y lo delega por MCP; Seelegent lo ejecuta como una llamada normal y no
interactiva a una CLI (`agy`, `claude` o `codex`) en la cuenta con más cuota disponible, comprueba el resultado y le
devuelve el informe al lead para que lo revise. Está pensado para quien tiene varias suscripciones o claves de API y
quiere que un agente planifique y revise mientras los demás escriben el código.

Seelegent no es un proxy ni agrupa cuentas detrás de un único punto de acceso. Cada ejecución es una llamada normal a
una CLI en una cuenta tuya, dentro de su propio perfil aislado, y los términos de cada proveedor siguen aplicándose a
cada cuenta.

## Funciones

- **Delega desde el lead que ya usas.** El lead llama a `delegate`, `wait`, `followup` y `review` por MCP, y
  Seelegent ejecuta cada turno como una llamada normal a la CLI del worker y devuelve su informe. →
  [Herramientas MCP](docs/mcp.md)
- **Primero, lo que te necesita.** La página **Panel** pone arriba las tareas con una pregunta, una sesión pendiente,
  un gate fallido o trabajo en riesgo, cada una con un aviso que dice qué hacer. →
  [Cuando algo se atasca](docs/getting-started.md#7-when-something-is-stuck)
- **Cada paso queda registrado.** Cada tarea tiene las vistas Terminal, Árbol, Grafo, Archivos y Git: la
  conversación, cada llamada a herramientas, los turnos y los intentos, y el diff con notas de revisión en sus líneas.
  → [Motor de tareas](docs/task-engine.md)
- **Muchas cuentas, un solo grupo.** Las sesiones propias, la sesión de este equipo o las claves de API se eligen por
  prioridad, tareas en marcha y cuota restante, y una tarea cuya cuenta se queda sin cuota pasa a otra con un
  traspaso. → [Configuración](docs/configuration.md)
- **Gates que devuelven el trabajo fallido.** Tras cada informe, el daemon pasa los tests, el lint y el build,
  devuelve un gate fallido al worker como turno de corrección automática, y la página **Pruebas** sigue qué requisitos
  de Spec Kit están probados. → [Gates, carriles y pruebas](docs/gates.md)
- **Tu trabajo sin confirmar se queda donde está.** La protección de Git rechaza los comandos de git que pueden tirar
  trabajo, y las instantáneas de antes y después de cada turno permiten restaurar los archivos que un turno pudo
  perder. → [Seguridad](docs/safety.md)
- **Un equipo, no solo workers.** Miembros con nombre y rango dan segundas opiniones, se reúnen antes de decisiones
  difíciles y convierten un brief en un concurso de 2 a 5 variantes en worktrees separados. → [Equipo](docs/team.md)
- **Del issue al pull request en borrador, o con un horario.** Clasifica issues de GitHub en tickets con su propio
  worktree y rama, abre un pull request en borrador tras revisar que no haya secretos y ejecuta delegaciones guardadas
  con un horario. → [GitHub](docs/github.md) · [Rutinas](docs/automations.md)
- **A la medida de tu equipo.** La concurrencia de gates, los umbrales de memoria y el paralelismo de los tests se
  calculan a partir de la memoria y las CPU de esta máquina; fija cualquiera de ellos, o vuelve al automático, en
  **Ajustes › Recursos**. → [Recursos](docs/configuration.md#resources)
- **Se configura con un comando y se revisa con otro.** `seelegent setup` registra el servidor MCP y la skill del lead
  en nueve hosts, y `seelegent doctor` revisa la instalación y explica cómo arreglar lo que falla. →
  [Configuración inicial](docs/setup.md) · [doctor](docs/cli.md#doctor)

### Míralo en acción

Cada vista previa enlaza a un vídeo corto.

<table>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/delegate.mp4"><img src="docs/media/delegate.webp" alt="El lead delega una tarea y recibe el informe del worker"></a><br>
      <b>Delegar</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/needs-you.mp4"><img src="docs/media/needs-you.webp" alt="La página Panel lista las tareas que te necesitan, cada una con su aviso"></a><br>
      <b>Te necesitan</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/task-detail.mp4"><img src="docs/media/task-detail.webp" alt="Las vistas Terminal, Árbol, Grafo, Archivos y Git de una tarea"></a><br>
      <b>Detalle de la tarea</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/accounts.mp4"><img src="docs/media/accounts.webp" alt="La página Cuentas con las ventanas de cuota y el inicio de sesión"></a><br>
      <b>Cuentas y cuota</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="Un gate fallido vuelve al worker como turno de corrección; la página Pruebas"></a><br>
      <b>Gates y pruebas</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="La protección de Git rechaza un comando peligroso; se restauran los archivos en riesgo"></a><br>
      <b>Protección de Git y restauración</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="Miembros, una consulta, una reunión y un concurso"></a><br>
      <b>Equipo</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/github-automations.mp4"><img src="docs/media/github-automations.webp" alt="Un issue convertido en ticket y pull request en borrador; el horario de una rutina"></a><br>
      <b>GitHub y rutinas</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/resources.mp4"><img src="docs/media/resources.webp" alt="Ajustes › Recursos: valores automáticos para esta máquina, Fijar y Usar automático"></a><br>
      <b>Recursos</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/setup-doctor.mp4"><img src="docs/media/setup-doctor.webp" alt="seelegent setup registra los hosts del lead; seelegent doctor revisa la instalación"></a><br>
      <b>Setup y doctor</b>
    </td>
  </tr>
</table>

## Requisitos

- **Sistema:** Windows 10 u 11, Linux o macOS, en x64 o ARM64. Windows es la plataforma más probada.
- **Git.** Seelegent lo necesita para las instantáneas, la vista Git, los concursos y los tickets de GitHub.
- **Al menos una CLI de worker** instalada: Antigravity CLI (`agy`, modelos Gemini, el backend por defecto), Claude
  Code (`claude`) o Codex (`codex`). Los backends `claude` y `codex` están apagados hasta que los actives en Ajustes.
- **Un host para el lead** con MCP: Claude Code, Claude Desktop, Codex CLI, OpenCode, Antigravity CLI o la aplicación
  Antigravity, Gemini CLI, Cursor o Windsurf. Un host sin MCP puede usar el comando `seelegent`.
- Opcional: la CLI de GitHub (`gh`) con sesión iniciada, para las funciones de GitHub.

## Instalación

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

**Linux y macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

El instalador descarga la última versión, comprueba su SHA-256 con el `checksums.txt` de la versión, pone `seelegent`
en el PATH (`%LOCALAPPDATA%\Programs\seelegent\bin` en Windows, `~/.local/bin` en los demás sistemas), importa los
datos de la versión antigua si los encuentra y ejecuta `seelegent setup` para los hosts del lead de esta máquina.
Después, abre una terminal nueva y reinicia los hosts del lead. Las opciones como `--version`, `--install-dir` y
`--no-setup` (sh) o `-Version`, `-InstallDir` y `-NoSetup` (PowerShell) están en
[docs/release.md](docs/release.md#installing). Para compilar desde el código fuente, consulta
[CONTRIBUTING.md](CONTRIBUTING.md#development-setup).

## Inicio rápido

1. **Comprueba la instalación:** `seelegent doctor`. Un `warn` es normal en una instalación nueva; un `fail` viene
   con la forma de arreglarlo.
2. **Conecta los hosts del lead:** el instalador ya ejecutó `seelegent setup`, que añade el servidor MCP `seelegent` a
   cada host del lead que encuentra e instala la skill del lead. Vuelve a ejecutarlo cuando instales un host nuevo
   (`--dry-run` muestra antes los cambios, `--remove` los deshace) y reinicia el host.
3. **Añade una cuenta:** `seelegent ui` arranca el daemon si hace falta y abre http://127.0.0.1:47821/. En **Cuentas**,
   elige **Añadir cuenta**, el backend y el tipo, y después **Iniciar sesión**: se ejecuta el inicio de sesión de la
   propia CLI del worker, en el navegador o en una ventana de terminal, y Seelegent nunca ve tu contraseña.
4. **Delega.** En la carpeta de un proyecto, pídele al lead algo como *«Usa Seelegent para añadir validación de datos
   al formulario de registro y luego revisa el diff»*. El lead llama a `delegate`, espera el informe, lee el diff, pasa
   los tests y después envía un `followup` o cierra la tarea con `review`. Desde una terminal:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

La [guía de uso](docs/usage.md) (en inglés) recorre el trabajo diario tarea por tarea, y
[docs/getting-started.md](docs/getting-started.md) (en inglés) explica en detalle la primera ejecución.

## Cómo funciona

```mermaid
flowchart LR
    U([Tú]) --> L[Agente lead]
    L -->|MCP: delegate, wait, followup, review| M[seelegent mcp]
    M -->|HTTP en 127.0.0.1| D[Daemon de Seelegent]
    D -->|una ejecución por turno| A[agy]
    D --> C[claude]
    D --> X[codex]
    U -->|navegador| B[Panel web]
    B --- D
```

- **Un solo binario.** `seelegent` es a la vez el daemon, la línea de comandos, el servidor MCP que arranca el host del
  lead (`seelegent mcp`) y el panel web.
- **El daemon** solo escucha en `127.0.0.1:47821`. Elige la cuenta, ejecuta el worker, registra cada paso, pasa los
  controles y sobrevive a los reinicios: el siguiente daemon retoma los workers que siguen en marcha.
- **Los workers** son las mismas CLI que ya usas, cada cuenta con su propia carpeta de perfil. Si una cuenta se queda
  sin cuota a mitad de una tarea, descansa hasta que se renueve su ventana y la tarea pasa a otra cuenta con un
  traspaso.
- **El panel web** (http://127.0.0.1:47821/) tiene las páginas Panel, Tareas, Issues, Reuniones, Concursos, Rutinas,
  Pruebas, Cuentas, Uso, Roles y Miembros, y un diálogo de Ajustes con búsqueda. Ofrece 26 temas de color, siete
  idiomas, una paleta de comandos (`Ctrl+K`, `Cmd+K` en macOS) y atajos de teclado. Va integrado en el binario y no
  carga nada de internet.
- **La línea de comandos.** Cada herramienta MCP salvo `profiles` tiene su comando (los roles de worker se editan en
  la página **Roles** y con la herramienta `profiles`) y todos los comandos aceptan `--json`: consulta
  `seelegent --help` y [docs/cli.md](docs/cli.md).

### Por dónde pasa una tarea

**Cuentas.** Tres backends (`agy`, `claude`, `codex`) y tres tipos de cuenta: un perfil aislado con su propia sesión,
la sesión que ya existe en este equipo (solo `agy`) o una clave de API de un proveedor (solo `claude` y `codex`:
Anthropic, DeepSeek, Moonshot Kimi, Z.ai GLM, OpenRouter, OpenAI, Groq, un Ollama local o un endpoint propio). El
grupo de cuentas lee las ventanas de cuota de cada cuenta y prefiere la de mayor prioridad, con menos tareas en marcha
y más cuota restante. Los niveles de modelo `fast`, `standard`, `strong` y `max` se asignan a modelos reales en cada
backend.

**Tareas.** Una tarea es una conversación: `delegate`, luego tantos `followup` como hagan falta y al final `review`
con `accepted` o `rejected`. Las tareas se ejecutan en paralelo, pueden esperarse entre sí (`after`), reciben en cada
brief la memoria del proyecto y el historial de revisiones, pueden ejecutarse con un rol de worker (reglas, estilo de
informe, skills, nivel por defecto) y pueden recibir la carpeta de una funcionalidad y los ids de tarea de Spec Kit en
lugar de toda la especificación. Las tareas que se atascan, entran en bucle o hacen una pregunta despiertan antes al
lead, y las que te necesitan (falta iniciar sesión, no quedan cuentas) suben al principio del panel. Puedes **tomar el
control** de una tarea en una consola y devolvérsela después al worker.

**Gates, carriles y pruebas.** Tras cada informe del worker, el daemon ejecuta el gate de la tarea: los comandos del
lead, las líneas `Gate:` de la especificación o los comandos de test, lint y build detectados en el repositorio, la
doctrina del proyecto, comprobaciones de sintaxis y la de higiene. Un gate fallido vuelve al worker como turno de
corrección automática (2 por defecto) antes de despertar al lead. Los carriles indican qué archivos puede cambiar una
tarea, para que los workers en paralelo no se pisen. Opcionalmente, una segunda revisión de solo lectura repasa el
cambio cuando el gate se ha superado.

**Equipo, GitHub y rutinas.** Los miembros son workers con nombre, con un backend, un modelo o nivel, un rol y un rango
(`worker`, `senior`, `peer`); en una consulta, la objeción de un par bloquea la aceptación hasta que el lead la
responda. Los hooks de eventos ejecutan tus propios comandos de shell ante eventos de tareas, cuentas y del daemon. Los
issues de un repositorio conectado se convierten en tickets; cuando el trabajo está aceptado, Seelegent hace commit,
sube la rama del ticket, abre un pull request en borrador y sigue sus checks de CI. Usa `gh` si tiene la sesión
iniciada, si no un token que guardas en el panel y, si tampoco, un modo manual con un enlace de comparación. Las
rutinas se ejecutan con un horario (cada hora, cada día, entre semana, cada semana o cron, en una zona horaria), con
un comando de comprobación previa y reglas de cuota.

### Herramientas MCP para el lead

| Herramienta | Qué hace |
|---|---|
| `delegate` | Inicia una tarea (o un concurso) y espera el informe del worker. |
| `wait` | Espera una o varias tareas; `any: true` vuelve con la primera. |
| `followup` | Envía la siguiente instrucción o las correcciones de una tarea. |
| `review` | Cierra una tarea como `accepted` o `rejected`, con notas. |
| `cancel` | Detiene una tarea en marcha o en cola. |
| `task` | El informe de una tarea y los últimos pasos del worker. |
| `status` | Cuentas y cuota, tareas activas, backends. |
| `memory` | Notas por proyecto que se incluyen en cada brief. |
| `profiles` | Roles de worker: reglas, estilo de informe, skills, nivel por defecto. |
| `proof` | Matriz de requisitos, ejecuciones de regresión, carriles reservados, estadísticas. |
| `members` | Los miembros del departamento. |
| `consult` | Una segunda opinión de un sénior o un par sobre una tarea terminada. |
| `notes` | Notas de revisión sobre el diff de una tarea, enviadas como un solo `followup`. |
| `meeting` | Una reunión estructurada de miembros antes de decidir. |
| `contest` | Seguir un concurso y elegir la ganadora. |
| `issues` | De issues de GitHub a tickets, worktrees y pull requests en borrador. |
| `automations` | Delegaciones programadas. |

`seelegent setup` también instala la skill del lead, que le enseña este ciclo; `seelegent skill` la muestra. Más
detalles en [docs/mcp.md](docs/mcp.md).

## Seguridad

Los workers trabajan en carpetas donde tú y otros workers tenéis cambios sin confirmar. Seelegent los protege por
capas; ninguna hace fallar una tarea por sí sola, sino que cuentan al lead y al panel lo que ha pasado. Consulta
[docs/safety.md](docs/safety.md).

- **Protección de Git.** Los workers no pueden ejecutar comandos de git que puedan tirar trabajo sin confirmar o
  reescribir el historial: `reset`, `restore`, `stash`, `checkout` (salvo `checkout -b` para crear una rama),
  `switch`, `clean`, `rebase`, `merge`, `pull`, `push`, `commit -a` y `--amend`, entre otros, también dentro de
  `bash -c`, PowerShell, tuberías hacia una shell o alias de git. El lead puede levantarla durante un turno con
  `allow_git`.
- **Instantáneas.** Antes y después de cada turno, Seelegent registra los archivos sin confirmar del repositorio sin
  tocar tu index, tu stash ni tus refs. Los archivos que el turno pudo perder aparecen *en riesgo*, con un botón para
  restaurarlos en el panel y el comando `seelegent restore`.
- **Comprobaciones tras el turno.** Los cambios de fin de línea o de BOM, los archivos sobrantes, las escrituras fuera
  del proyecto y los solapamientos con otras tareas se señalan en la tarea.
- **Lo que un worker no puede hacer.** Un worker no puede llamar a Seelegent (no puede crear, cambiar ni revisar
  tareas), no puede activar una rutina y no puede guardar un token de GitHub. Las dos últimas cosas solo puedes
  hacerlas tú, en el panel.
- **Antes de un push.** Seelegent sube un único commit propio, nunca los commits del worker, y solo después de revisar
  cada archivo que añade: archivos `.env`, claves y certificados, archivos de credenciales, tokens dentro del
  contenido, nombres de dispositivo de Windows como `nul` y archivos de más de 5 MB impiden el push, sin forma de
  saltárselo. Nunca hace force-push, ni merge, ni cierra issues.
- **Las rutinas empiezan apagadas.** Toda rutina se crea desactivada. Solo se ejecuta cuando confirmas en el panel
  exactamente lo que hace, y cualquier cambio en lo que ejecuta o en cuándo lo hace la vuelve a apagar.

Son protecciones, no un sandbox: las CLI de los workers se ejecutan con tu usuario. Revisa lo que cambian. Para
informar de un problema de seguridad, consulta [SECURITY.md](SECURITY.md).

## Datos y privacidad

- Todo se ejecuta en tu equipo. El daemon solo escucha en `127.0.0.1`. Seelegent en sí no envía telemetría; las CLI
  de los workers siguen sus propios ajustes.
- Los datos están en `~/.seelegent` (define `SEELEGENT_HOME` para moverlos): `config.json` (ajustes), `seelegent.db`
  (tareas, cuentas, uso, equipo, tickets, rutinas), `profiles/` (un perfil aislado por cuenta, con su clave de API),
  `github/` (el token), `snapshots/`, `contests/` (los diffs guardados de los concursos), `worktrees/` (solo para
  concursos y tickets), `run/` y `logs/`. Consulta [docs/configuration.md](docs/configuration.md).
- Seelegent nunca pide contraseñas. Cada CLI de worker inicia sesión con su propio flujo y guarda sus tokens en el
  perfil de la cuenta. Las claves de API y el token de GitHub se guardan cada uno en su propio archivo, cifrados con
  DPAPI en Windows y como texto plano legible solo por su dueño (0600) en los demás sistemas; el panel muestra solo los
  cuatro últimos caracteres y los registros ocultan los secretos.
- Lo que sale de tu equipo: las CLI de los workers hablan con sus propios proveedores (ese es el trabajo), la cuota se
  lee a través de las CLI (y, en las cuentas de Claude con sesión, del endpoint de uso de Anthropic con la propia sesión
  de esa cuenta), las cuentas con clave de API llaman al proveedor que configuraste, y a GitHub solo se le llama por los
  repositorios que conectas (sincronización, una consulta de sus issues y pull requests cada 10 minutos, que
  `githubPollMinutes: 0` desactiva, y los push, pull requests y comentarios que pides tú o el lead), para comprobar tu
  sesión de GitHub cuando abres **Issues**, y desde los instaladores y `seelegent update`.

## Actualizar y desinstalar

`seelegent update` te dice si hay una versión nueva; `seelegent update --apply` la instala en el mismo sitio
(comprobada con `checksums.txt`, con el daemon reiniciado y los hosts del lead actualizados) y
`seelegent update --rollback` vuelve atrás una vez. La base de datos nunca se migra hacia atrás, así que se rechaza una
versión más antigua que tus datos. Consulta [docs/release.md](docs/release.md#updating).

Para desinstalar:

1. `seelegent stop` y después `seelegent setup --remove` (quita las entradas MCP, la skill del lead y el hook).
2. Borra el binario y el `seelegent.exe.previous` (`seelegent.previous`) que se guarda a su lado:
   `%LOCALAPPDATA%\Programs\seelegent\bin\` en Windows, `~/.local/bin/seelegent*` en los demás sistemas. Quita esa
   carpeta del PATH (el `Path` de usuario en Windows; en los demás, la línea `export PATH=...` que el instalador añadió
   al perfil de tu shell).
3. Si quieres, borra `~/.seelegent`. Contiene los perfiles de tus cuentas (con sus sesiones), las claves, las tareas y
   el historial.

### Migrar desde la versión antigua

La antigua versión en Node guardaba sus datos en la misma carpeta. En la primera instalación, el instalador ejecuta
`seelegent migrate`, que importa los ajustes, los proveedores, las cuentas con sus perfiles y claves de API, la memoria
de los proyectos y el historial de revisiones, los roles, los miembros, los hooks, las actas de reuniones y las rutinas
(todas apagadas, para confirmarlas de nuevo). No se importan las tareas, las notas de revisión, los concursos, los
registros de pruebas, el historial de uso ni la carpeta de GitHub: vuelve a conectar tus repositorios y a guardar el
token. La carpeta antigua nunca se modifica. En Windows, si el lanzador antiguo está en la carpeta de instalación, se
conserva como `seelegent-node`. Detén el daemon antiguo antes de instalar y ejecuta `seelegent migrate --dry-run` para
ver qué se importaría. Consulta [docs/setup.md](docs/setup.md#seelegent-migrate).

## Solución de problemas

- `seelegent doctor` revisa la carpeta de datos, los ajustes, la base de datos, el daemon, git, cada CLI de worker
  activada, las cuentas, el PATH y la memoria libre frente a los umbrales de recursos, y explica cómo arreglar lo que
  falla. No cambia nada, salvo una prueba de escritura en la carpeta de datos (`.doctor-write-test`, que borra
  enseguida).
- `seelegent status` muestra el daemon, las cuentas con su cuota y las tareas que te necesitan; `seelegent restart`
  reinicia el daemon. Su registro es `~/.seelegent/logs/daemon.log`.
- `seelegent setup --dry-run` muestra lo que hay registrado en cada host del lead. Reinicia el host después de un setup
  o una actualización.
- «Seelegent is not available inside a Seelegent worker.» significa que un worker intentó llamar a Seelegent; se
  rechaza a propósito.
- Una tarea en **Te necesitan** explica el motivo en su aviso: responde a su pregunta, inicia sesión en una cuenta,
  restaura los archivos en riesgo o pide más cambios si el gate ha fallado.

## Documentación

La documentación está en inglés.

- [Guía de uso](docs/usage.md): el trabajo diario, tarea por tarea.
- [Primeros pasos](docs/getting-started.md): la primera ejecución, paso a paso.
- [Índice de la documentación](docs/README.md): configuración inicial, ajustes, seguridad, GitHub, rutinas, la
  referencia para el lead (herramientas MCP, gates, equipo), la línea de comandos, la API HTTP, el motor de tareas y la
  guía para contribuir.

## Contribuir y seguridad

Las contribuciones son bienvenidas: [CONTRIBUTING.md](CONTRIBUTING.md) explica el entorno de desarrollo, las
comprobaciones que debe superar un pull request y el estilo de los commits. Todas las personas que participan siguen
el [Código de conducta](CODE_OF_CONDUCT.md). Informa de los problemas de seguridad en privado, como describe
[SECURITY.md](SECURITY.md), y no en un issue público.

## Licencia

El código de Seelegent está disponible (source-available) bajo la
[Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-ALv2)](LICENSE).

- Puedes usarlo, copiarlo, modificarlo y compartirlo con cualquier fin que no sea un uso competidor, también dentro de
  una empresa, para educación e investigación no comerciales y en servicios profesionales que prestes a quien lo use.
- Un uso competidor es ofrecer Seelegent a terceros en un producto o servicio comercial que lo sustituya (o sustituya
  otro producto o servicio que el licenciante ofrezca con él) o que ofrezca la misma funcionalidad o una
  sustancialmente similar.
- Dos años después de publicarse cada versión, esa versión también queda disponible bajo la Apache License 2.0.

El archivo [LICENSE](LICENSE) contiene los términos exactos. Las obras de terceros incluidas en Seelegent se enumeran
en [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
