<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/media/logo-dark.svg">
  <img src="docs/media/logo-light.svg" alt="Seelegent" width="372">
</picture>

**Un agente principal planifica y revisa. Las CLI de programación de tus propias cuentas escriben el código.**

[Instalación](#instalación)&nbsp;· [Inicio rápido](#inicio-rápido)&nbsp;· [Guía de uso](docs/usage.md)&nbsp;·
[Documentación](docs/README.md)&nbsp;· [Contribuir](CONTRIBUTING.md)

[English](README.md)&nbsp;· [Tiếng Việt](README.vi.md)&nbsp;· [简⁠体⁠中⁠文](README.zh-CN.md)&nbsp;· **Español**&nbsp;·
[한⁠국⁠어](README.ko.md)&nbsp;· [日⁠本⁠語](README.ja.md)&nbsp;· [Русский](README.ru.md)

[![License: FSL-1.1-ALv2](https://img.shields.io/badge/license-FSL--1.1--ALv2-blue)](LICENSE)
[![Latest release](https://img.shields.io/github/v/release/NovaSeele/Seelegent)](https://github.com/NovaSeele/Seelegent/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/NovaSeele/Seelegent)](go.mod)
[![Platforms: Windows | Linux | macOS](https://img.shields.io/badge/platforms-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#requisitos)
[![CI](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml/badge.svg)](https://github.com/NovaSeele/Seelegent/actions/workflows/ci.yml)

</div>

<!-- Hero: at publish time, replace this block with the uploaded user-attachments video URL on its own line. -->
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
  worktree y rama, abre un pull request en borrador tras revisar que no haya secretos cuando tú o el lead lo piden y
  ejecuta delegaciones guardadas con un horario. → [GitHub](docs/github.md) · [Rutinas](docs/automations.md)

### Míralo en acción

Cada vista previa enlaza a un vídeo corto; la [guía de uso](docs/usage.md) tiene más.

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
      <a href="docs/media/gates-proof.mp4"><img src="docs/media/gates-proof.webp" alt="Un gate fallido vuelve al worker como turno de corrección; la página Pruebas"></a><br>
      <b>Gates y pruebas</b>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <a href="docs/media/git-guard.mp4"><img src="docs/media/git-guard.webp" alt="La protección de Git rechaza un comando peligroso; se restauran los archivos en riesgo"></a><br>
      <b>Protección de Git y restauración</b>
    </td>
    <td width="50%" align="center">
      <a href="docs/media/team.mp4"><img src="docs/media/team.webp" alt="Miembros, una consulta, una reunión y un concurso"></a><br>
      <b>Equipo</b>
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

1. **Comprueba la instalación:** `seelegent doctor` revisa la instalación y explica cómo arreglar lo que falla. Un
   `warn` es normal en una instalación nueva; un `fail` viene con la forma de arreglarlo.
2. **Conecta los hosts del lead:** el instalador ya ejecutó `seelegent setup`, que registra el servidor MCP
   `seelegent` en cada host del lead que encuentra, de los nueve que conoce, e instala la skill del lead en Claude
   Code, Codex CLI, OpenCode y Antigravity CLI. Vuelve a ejecutarlo
   cuando instales un host nuevo (`--dry-run` muestra antes los cambios, `--remove` los deshace) y reinicia el host. →
   [Configuración inicial](docs/setup.md)
3. **Añade una cuenta:** `seelegent ui` arranca el daemon si hace falta y abre http://127.0.0.1:47821/. En **Cuentas**,
   elige **Añadir cuenta**, el backend y el tipo. En una cuenta con su propio inicio de sesión, el de la propia CLI
   del worker empieza enseguida, en el navegador o en una ventana de terminal (**Iniciar sesión** en la tarjeta lo
   reintenta), y Seelegent nunca ve tu contraseña.
4. **Delega.** En la carpeta de un proyecto, pídele al lead algo como *«Usa Seelegent para añadir validación de datos
   al formulario de registro y luego revisa el diff»*. El lead llama a `delegate`, espera el informe, lee el diff, pasa
   los tests y después envía un `followup` o cierra la tarea con `review`. Desde una terminal:
   `seelegent delegate --cwd . --model fast "Describe this project in five lines"`.

La [guía de uso](docs/usage.md) (en inglés) recorre el trabajo diario tarea por tarea, y
[docs/getting-started.md](docs/getting-started.md) (en inglés) explica en detalle la primera ejecución.

## Configuración

Los ajustes están en el diálogo **Ajustes** del panel y en `~/.seelegent/config.json`. La concurrencia de gates,
los umbrales de memoria y el paralelismo de los tests se calculan por defecto a partir de la memoria y las CPU de esta
máquina; fija cualquiera de ellos, o vuelve al automático, en **Ajustes › Recursos**. →
[Configuración](docs/configuration.md) · [Recursos](docs/configuration.md#resources)

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
- **El panel web** (http://127.0.0.1:47821/) va integrado en el binario y no carga nada de internet. Está en siete
  idiomas y sigue el idioma de tu navegador, salvo que elijas uno en **Ajustes › Idioma**. Cada herramienta MCP salvo
  `profiles` tiene también su comando `seelegent` ([docs/cli.md](docs/cli.md)).

La [guía de uso](docs/usage.md#what-a-task-goes-through) (en inglés) explica por dónde pasa una tarea y enumera las
17 herramientas MCP.

## Seguridad

La protección de Git rechaza los comandos de git que pueden tirar trabajo sin confirmar, las instantáneas de antes y
después de cada turno permiten restaurar lo que un turno pudo perder, y Seelegent revisa cada archivo antes de subir
la rama de un ticket. Son protecciones, no un sandbox: las CLI de los workers se ejecutan con tu usuario, así que
revisa lo que cambian. Consulta [docs/safety.md](docs/safety.md), y [SECURITY.md](SECURITY.md) para informar de un
problema de seguridad.

## Datos y privacidad

Todo se ejecuta en tu equipo: el daemon solo escucha en `127.0.0.1`, Seelegent no envía telemetría y nunca pide
contraseñas. Los datos están en `~/.seelegent` (`SEELEGENT_HOME` los mueve). Qué se guarda ahí y qué sale de tu
equipo: [guía de uso](docs/usage.md#data-and-privacy).

## Actualizar y desinstalar

`seelegent update --apply` instala una versión nueva en el mismo sitio y `seelegent update --rollback` vuelve atrás
una vez. `seelegent setup --remove` deshace el registro en los hosts antes de borrar el binario. Todos los pasos, y
cómo migrar desde la antigua versión en Node: [guía de uso](docs/usage.md#update-and-uninstall).

## Solución de problemas

Ejecuta primero `seelegent doctor` y después `seelegent status`; el registro del daemon es
`~/.seelegent/logs/daemon.log`. Una tarea en **Te necesitan** dice en su aviso qué hacer. Más:
[guía de uso](docs/usage.md#doctor-and-troubleshooting).

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
