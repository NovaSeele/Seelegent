<#
.SYNOPSIS
    Installs Seelegent on Windows: downloads a release, verifies its SHA-256, installs seelegent.exe, puts it on
    the user PATH, imports the old (Node) version's data and registers Seelegent with the lead hosts.

.DESCRIPTION
    Works in Windows PowerShell 5.1 and PowerShell 7, as a file or piped into Invoke-Expression:

        irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex

    Parameters (or the environment variables in brackets, which also work with irm | iex):
      -Version v1.2.3     the release to install (SEELEGENT_VERSION); default: the latest release
      -InstallDir <dir>   where seelegent.exe goes (SEELEGENT_INSTALL_DIR);
                          default: %LOCALAPPDATA%\Programs\seelegent\bin
      -NoPath             do not add the folder to the user PATH (SEELEGENT_NO_MODIFY_PATH=1)
      -NoSetup            do not run `seelegent setup` (SEELEGENT_NO_SETUP=1)
      -Force              install a release that does not report which database schema it knows
                          (SEELEGENT_FORCE=1); a release known to be too old for the database is refused even then
    SEELEGENT_INSTALL_URL (else SEELEGENT_UPDATE_URL) replaces the release information address
    https://api.github.com/repos/NovaSeele/Seelegent/releases/latest, for example with a local test server.

    Any failure stops the script with an error (exit code 1 when run as a file). See docs/release.md.

.EXAMPLE
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1))) -Version v1.2.3
#>

# Everything runs inside this function so that, under irm | iex, no setting or variable leaks into the caller's
# session. The file is plain ASCII: Windows PowerShell 5.1 reads a file without a BOM in the ANSI code page.
function Install-Seelegent {
    [CmdletBinding()]
    param(
        [string] $Version = $env:SEELEGENT_VERSION,
        [string] $InstallDir = $env:SEELEGENT_INSTALL_DIR,
        [switch] $NoPath,
        [switch] $NoSetup,
        [switch] $Force
    )

    Set-StrictMode -Version 2.0
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is very slow in 5.1 with the progress bar
    if ($env:SEELEGENT_WORKER -eq '1') {
        # A worker must not replace the executable or stop the daemon that runs it (old C-B12).
        throw 'Seelegent is not available inside a Seelegent worker.'
    }
    if ($env:SEELEGENT_NO_MODIFY_PATH -eq '1') { $NoPath = $true }
    if ($env:SEELEGENT_NO_SETUP -eq '1') { $NoSetup = $true }
    if ($env:SEELEGENT_FORCE -eq '1') { $Force = $true }

    $repoLatest = 'https://api.github.com/repos/NovaSeele/Seelegent/releases/latest'
    $userAgent = 'seelegent-installer'

    function Write-Step([string] $Message) { Write-Host $Message -ForegroundColor Cyan }
    function Write-Note([string] $Message) { Write-Host "Warning: $Message" -ForegroundColor Yellow }

    # Accepts https, and http only on this machine (a local test server): exactly 127.0.0.1, localhost or [::1].
    # An address with user information (user@host) is refused.
    function Assert-Url([string] $Url) {
        $u = $null
        if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref] $u)) { throw "Not an address: $Url" }
        if ($u.UserInfo -or $Url -match '^[^/]*//[^/?#]*[@\\]') { throw "Not an allowed address: $Url" }
        if ($u.Scheme -eq 'https') { return }
        if ($u.Scheme -eq 'http' -and @('127.0.0.1', 'localhost', '[::1]', '::1') -contains $u.Host) { return }
        throw "Only https addresses (or http on this machine) are allowed: $Url"
    }

    # Reads $Url into $OutFile, or returns its text when $OutFile is empty. Redirects (at most five) are followed
    # here, not by the HTTP stack, so every address on the way passes Assert-Url: no plain http except on this
    # machine (old C-B15). The same code runs in Windows PowerShell 5.1 and PowerShell 7.
    function Get-Url([string] $Url, [string] $OutFile = '', [string] $Accept = '*/*') {
        $current = $Url
        for ($hop = 0; $hop -le 5; $hop++) {
            Assert-Url $current
            $request = [System.Net.HttpWebRequest]::Create($current)
            $request.AllowAutoRedirect = $false
            $request.UserAgent = $userAgent
            $request.Accept = $Accept
            $request.Timeout = 60000
            $request.ReadWriteTimeout = 300000
            $response = $null
            try {
                try {
                    $response = $request.GetResponse()
                } catch [System.Net.WebException] {
                    if ($null -eq $_.Exception.Response) { throw "Could not read ${current}: $($_.Exception.Message)" }
                    $response = $_.Exception.Response
                }
                $code = [int] $response.StatusCode
                if ($code -ge 300 -and $code -lt 400) {
                    $location = $response.Headers['Location']
                    if (-not $location) { throw "$current redirects without saying where to." }
                    $current = (New-Object System.Uri -ArgumentList ([Uri] $current), $location).AbsoluteUri
                    continue
                }
                if ($code -lt 200 -or $code -ge 300) { throw "$current answered HTTP $code." }
                $stream = $response.GetResponseStream()
                if ($OutFile) {
                    $file = [System.IO.File]::Create($OutFile)
                    try { $stream.CopyTo($file) } finally { $file.Dispose() }
                    return
                }
                $reader = New-Object System.IO.StreamReader -ArgumentList $stream, ([System.Text.Encoding]::UTF8)
                try { return $reader.ReadToEnd() } finally { $reader.Dispose() }
            } finally {
                if ($response) { $response.Close() }
            }
        }
        throw "$Url redirects more than five times."
    }

    function Get-Tag([string] $Value) {
        $v = $Value.Trim() -replace '^[vV]', ''
        if ($v -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$') {
            throw "'$Value' is not a version like v1.2.3"
        }
        return 'v' + ($v -replace '\+.*$', '')
    }

    function Get-Arch {
        $a = $null
        try { $a = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { $a = $null }
        if (-not $a) {
            $a = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        }
        switch -Regex ($a) {
            '^(X64|AMD64)$' { return 'amd64' }
            '^ARM64$' { return 'arm64' }
        }
        throw "This processor ($a) has no Seelegent build; releases exist for amd64 and arm64."
    }

    # Runs seelegent with the error preference relaxed: Windows PowerShell 5.1 turns a native program's redirected
    # stderr into errors. Returns stdout as one string; $LASTEXITCODE is the program's exit code.
    function Invoke-Quiet([string] $Exe, [string[]] $Arguments) {
        $ErrorActionPreference = 'Continue'
        $out = & $Exe @Arguments 2>$null
        return ($out | Out-String)
    }

    function Get-FreeName([string] $Path) {
        return $Path + '.old-' + [Guid]::NewGuid().ToString('N').Substring(0, 12)
    }

    # Removes executables a former install or update could not delete while they ran.
    function Remove-Leftovers([string] $Exe) {
        $dir = Split-Path -Parent $Exe
        $name = Split-Path -Leaf $Exe
        Get-ChildItem -LiteralPath $dir -Force -ErrorAction SilentlyContinue | Where-Object {
            $_.Name -like "$name.old-*" -or $_.Name -eq "$name.new"
        } | ForEach-Object { Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue }
    }

    # Puts $New in place of $Exe. The replaced executable is kept as <exe>.previous for `seelegent update
    # --rollback`. Only renames in one folder, so a running seelegent.exe can be replaced; on failure the old
    # executable (and an older kept one) is put back. An older kept executable is deleted only at the end.
    function Install-Executable([string] $New, [string] $Exe) {
        $staged = "$Exe.new"
        $previous = "$Exe.previous"
        if (Test-Path -LiteralPath $staged) { Remove-Item -LiteralPath $staged -Force }
        Copy-Item -LiteralPath $New -Destination $staged
        if (-not (Test-Path -LiteralPath $Exe)) {
            [System.IO.File]::Move($staged, $Exe)
            return $null
        }
        $trashedPrevious = $null
        if (Test-Path -LiteralPath $previous) {
            $trashedPrevious = Get-FreeName $Exe
            try {
                [System.IO.File]::Move($previous, $trashedPrevious)
            } catch {
                Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
                throw "Could not move the older $previous aside: $($_.Exception.Message). Nothing was changed."
            }
        }
        try {
            [System.IO.File]::Move($Exe, $previous)
        } catch {
            if ($trashedPrevious) { [System.IO.File]::Move($trashedPrevious, $previous) }
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
            throw "Could not move the installed $Exe aside: $($_.Exception.Message). Nothing was changed."
        }
        try {
            [System.IO.File]::Move($staged, $Exe)
        } catch {
            [System.IO.File]::Move($previous, $Exe)
            if ($trashedPrevious) { [System.IO.File]::Move($trashedPrevious, $previous) }
            throw "Could not put the new seelegent.exe in place: $($_.Exception.Message). The old one is back."
        }
        if ($trashedPrevious) { Remove-Item -LiteralPath $trashedPrevious -Force -ErrorAction SilentlyContinue }
        return $previous
    }

    # The old (Node) installer wrote bin\seelegent.cmd, which starts node ...\src\cli.js. It is renamed to
    # seelegent-node.cmd so that `seelegent` always means the new executable while the old version stays runnable.
    function Move-OldShim([string] $Dir) {
        $shim = Join-Path $Dir 'seelegent.cmd'
        if (-not (Test-Path -LiteralPath $shim)) { return $null }
        $text = [System.IO.File]::ReadAllText($shim)
        if ($text -notmatch 'cli\.js') {
            Write-Note "$shim is not the old version's launcher; it was left alone. Remove it if it is not yours."
            return $null
        }
        $target = Join-Path $Dir 'seelegent-node.cmd'
        if (Test-Path -LiteralPath $target) { $target = Get-FreeName $shim }
        [System.IO.File]::Move($shim, $target)
        return $target
    }

    function Test-PathListHas([string] $List, [string] $Dir) {
        $want = [Environment]::ExpandEnvironmentVariables($Dir).TrimEnd('\')
        foreach ($entry in ($List -split ';')) {
            $e = [Environment]::ExpandEnvironmentVariables($entry.Trim()).TrimEnd('\')
            if ($e -and $e -ieq $want) { return $true }
        }
        return $false
    }

    # Adds $Dir to the user PATH in the registry, keeping its type (REG_EXPAND_SZ) and its %VARIABLES% unexpanded,
    # then tells running programs that the environment changed. Returns $true when it changed something.
    # $Key replaces HKCU\Environment (tests pass an object with GetValue, SetValue and Close).
    function Add-UserPath([string] $Dir, $Key = $null) {
        $real = $null -eq $Key
        if ($real) { $Key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true) }
        if ($null -eq $Key) { throw 'Cannot open HKCU\Environment to update PATH.' }
        try {
            $raw = [string] $Key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            if (Test-PathListHas $raw $Dir) { return $false }
            $new = if ($raw.Trim()) { $raw.TrimEnd(';') + ';' + $Dir } else { $Dir }
            $Key.SetValue('Path', $new, [Microsoft.Win32.RegistryValueKind]::ExpandString)
        } finally {
            $Key.Close()
        }
        if ($real) {
            # Setting a user variable broadcasts WM_SETTINGCHANGE, so Explorer and new terminals see the new PATH.
            [Environment]::SetEnvironmentVariable('SEELEGENT_INSTALLER', '1', 'User')
            [Environment]::SetEnvironmentVariable('SEELEGENT_INSTALLER', $null, 'User')
        }
        return $true
    }

    function Find-Asset($Release, [string] $Name) {
        if (-not $Release.PSObject.Properties['assets']) { return $null }
        foreach ($a in @($Release.assets)) {
            if ($a.name -eq $Name) { return [string] $a.browser_download_url }
        }
        return $null
    }

    function Get-ExpectedSum([string] $SumsFile, [string] $Name) {
        foreach ($line in [System.IO.File]::ReadAllLines($SumsFile)) {
            if ($line.Trim() -match '^([0-9a-fA-F]{64})\s+\*?(.+)$' -and $Matches[2].Trim() -eq $Name) {
                return $Matches[1].ToLowerInvariant()
            }
        }
        throw "checksums.txt does not list $Name; refusing to install a file that cannot be verified."
    }

    function Expand-Executable([string] $Zip, [string] $Destination) {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [System.IO.Compression.ZipFile]::OpenRead($Zip)
        try {
            foreach ($entry in $archive.Entries) {
                $parts = $entry.FullName.Replace('\', '/').TrimStart('.', '/').Split('/')
                if ($entry.FullName -notmatch '\.\.' -and $parts.Count -le 2 -and $parts[-1] -eq 'seelegent.exe') {
                    [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $Destination, $true)
                    return
                }
            }
        } finally {
            $archive.Dispose()
        }
        throw "$Zip holds no seelegent.exe."
    }

    # --- release information -------------------------------------------------------------------------------------
    if ([Net.ServicePointManager]::SecurityProtocol -ne 0) {
        # Windows PowerShell 5.1 may still default to TLS 1.0; GitHub needs TLS 1.2.
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor
            [Net.SecurityProtocolType]::Tls12
    }
    $arch = Get-Arch
    $tag = $null
    if ($Version) { $tag = Get-Tag $Version }
    $metaUrl = if ($env:SEELEGENT_INSTALL_URL) { $env:SEELEGENT_INSTALL_URL }
    elseif ($env:SEELEGENT_UPDATE_URL) { $env:SEELEGENT_UPDATE_URL }
    else { $repoLatest }
    if ($tag -and $metaUrl.TrimEnd('/') -match '/releases/latest$') {
        $metaUrl = ($metaUrl.TrimEnd('/') -replace '/releases/latest$', '') + "/releases/tags/$tag"
    }
    Assert-Url $metaUrl

    Write-Step "Reading the release information from $metaUrl ..."
    try {
        $release = Get-Url $metaUrl '' 'application/vnd.github+json' | ConvertFrom-Json
    } catch {
        throw "Could not read the release information from ${metaUrl}: $($_.Exception.Message)"
    }
    $rawTag = $null
    foreach ($field in @('tag_name', 'name')) {
        if (-not $rawTag -and $release.PSObject.Properties[$field]) { $rawTag = $release.$field }
    }
    if (-not $rawTag) { throw "The release information at $metaUrl names no version." }
    $relTag = Get-Tag ([string] $rawTag)
    if ($tag -and $relTag -ne $tag) { throw "The release at $metaUrl is $relTag, not $tag." }
    $plain = $relTag.Substring(1)
    $assetName = "seelegent_${plain}_windows_$arch.zip"
    $assetUrl = Find-Asset $release $assetName
    $sumsUrl = Find-Asset $release 'checksums.txt'
    if (-not $assetUrl) { throw "Release $relTag has no $assetName." }
    if (-not $sumsUrl) { throw "Release $relTag has no checksums.txt; refusing to install a file that cannot be verified." }
    Assert-Url $assetUrl
    Assert-Url $sumsUrl

    if (-not $InstallDir) {
        if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set; pass -InstallDir.' }
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\seelegent\bin'
    }
    $InstallDir = [System.IO.Path]::GetFullPath($InstallDir)
    if ($InstallDir -match '[;"]') { throw "The install folder may not contain ; or a double quote: $InstallDir" }
    $exe = Join-Path $InstallDir 'seelegent.exe'
    $dataDir = if ($env:SEELEGENT_HOME) { $env:SEELEGENT_HOME } else { Join-Path $env:USERPROFILE '.seelegent' }

    $temp = Join-Path ([System.IO.Path]::GetTempPath()) ('seelegent-install-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temp -Force | Out-Null
    $failed = @()
    try {
        # --- download and verify --------------------------------------------------------------------------------
        $zip = Join-Path $temp $assetName
        $sums = Join-Path $temp 'checksums.txt'
        Write-Step "Downloading $assetName ..."
        Get-Url $assetUrl $zip
        Get-Url $sumsUrl $sums
        $want = Get-ExpectedSum $sums $assetName
        $got = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($got -ne $want) {
            throw "SHA-256 mismatch for ${assetName}: expected $want, got $got. The download is damaged or was changed; nothing was installed."
        }
        Write-Step 'Checksum verified (SHA-256).'
        $newExe = Join-Path $temp 'seelegent.exe'
        Expand-Executable $zip $newExe
        $info = Invoke-Quiet $newExe @('version', '--json')
        $reported = $null
        try { $reported = ($info | ConvertFrom-Json).version } catch { $reported = $null }
        if ($LASTEXITCODE -ne 0 -or $reported -ne $relTag) {
            throw "The downloaded seelegent.exe does not run here or is not $relTag (it reported '$reported'); nothing was installed."
        }
        # A release that knows fewer database migrations than the database has could not open it (migrations are
        # never undone). The downloaded executable checks the database itself, read-only.
        if (Test-Path -LiteralPath (Join-Path $dataDir 'seelegent.db')) {
            $check = Invoke-Quiet $newExe @('version', '--check-database', '--json')
            if ($LASTEXITCODE -ne 0) {
                $code = $null
                try { $code = ($check | ConvertFrom-Json).code } catch { $code = $null }
                if ($code -eq 'schema_too_new') {
                    throw "The database in $dataDir is newer than $relTag can open (migrations are never undone); install a newer release. Nothing was installed."
                }
                if (-not $Force) {
                    throw "$relTag does not report which database schema it knows, so it may not open the database in $dataDir; -Force installs it anyway. Nothing was installed."
                }
                Write-Note "$relTag does not report which database schema it knows; installing it anyway (-Force)."
            }
        }

        # --- install --------------------------------------------------------------------------------------------
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        $wasRunning = $false
        if (Test-Path -LiteralPath $exe) {
            Remove-Leftovers $exe
            $stop = Invoke-Quiet $exe @('stop', '--json')
            try { $wasRunning = [bool] ($stop | ConvertFrom-Json).stopped } catch { $wasRunning = $false }
            if ($LASTEXITCODE -ne 0) {
                Write-Note "the running Seelegent daemon could not be stopped: $($stop.Trim())"
            } elseif ($wasRunning) {
                Write-Step 'Stopped the running daemon.'
            }
        }
        Write-Step "Installing $exe ..."
        try {
            $previous = Install-Executable $newExe $exe
        } catch {
            if ($wasRunning) {
                # The old executable is back in place: start the daemon that was stopped for the install again.
                Write-Note 'the install failed; starting the daemon again ...'
                $null = Invoke-Quiet $exe @('restart')
                if ($LASTEXITCODE -ne 0) { Write-Note "the daemon could not be started again; run: $exe restart" }
            }
            throw
        }
        if ($previous) { Write-Step "The previous executable is kept as $previous (seelegent update --rollback)." }
        $shim = Move-OldShim $InstallDir
        if ($shim) { Write-Step "The old version's launcher seelegent.cmd is now $shim." }

        # --- PATH -----------------------------------------------------------------------------------------------
        $pathChanged = $false
        if ($NoPath) {
            Write-Step 'PATH left alone (-NoPath).'
        } else {
            $pathChanged = Add-UserPath $InstallDir
            if ($pathChanged) { Write-Step "Added $InstallDir to your user PATH." }
        }
        if (-not (Test-PathListHas $env:Path $InstallDir)) { $env:Path = "$InstallDir;$env:Path" }
        $found = Get-Command seelegent -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found -and $found.Source -ine $exe) {
            Write-Note "another seelegent comes first on PATH: $($found.Source). Remove it or move $InstallDir before it."
        }

        # --- data and hosts -------------------------------------------------------------------------------------
        $oldData = @('settings.json', 'accounts.json', 'roles.json', 'memory') |
            Where-Object { Test-Path -LiteralPath (Join-Path $dataDir $_) }
        if ($oldData -and -not (Test-Path -LiteralPath (Join-Path $dataDir 'seelegent.db'))) {
            Write-Step "Importing the old version's data from $dataDir (seelegent migrate) ..."
            & $exe migrate
            if ($LASTEXITCODE -ne 0) { $failed += 'seelegent migrate' }
        }
        if ($NoSetup) {
            Write-Step 'Lead hosts left alone (-NoSetup).'
        } else {
            Write-Step 'Registering Seelegent with the lead hosts (seelegent setup) ...'
            & $exe setup
            if ($LASTEXITCODE -ne 0) { $failed += 'seelegent setup' }
        }
        if ($wasRunning) {
            & $exe restart
            if ($LASTEXITCODE -ne 0) { $failed += 'seelegent restart' }
        }
        $status = Invoke-Quiet $exe @('status', '--json')
        if ($LASTEXITCODE -ne 0) {
            $msg = $status.Trim()
            try { $msg = ($status | ConvertFrom-Json).error } catch { $msg = $status.Trim() }
            Write-Note "the daemon cannot start yet: $msg"
            if ($shim) { Write-Note "if the old version's daemon still runs, stop it with: $(Split-Path -Leaf $shim) stop" }
        }
    } finally {
        Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Host ''
    Write-Host "Seelegent $relTag is installed: $exe" -ForegroundColor Green
    Write-Host 'Next steps:'
    if ($pathChanged) { Write-Host '  - Open a new terminal so it picks up the new PATH.' }
    if ($NoPath -and -not (Test-PathListHas ([Environment]::GetEnvironmentVariable('Path', 'User')) $InstallDir)) {
        Write-Host "  - Add $InstallDir to PATH, or run $exe directly."
    }
    if ($NoSetup) { Write-Host '  - Run `seelegent setup` to register Seelegent with your lead hosts.' }
    Write-Host '  - Restart your lead hosts (Claude Code, Codex, ...) so they load the Seelegent MCP server.'
    Write-Host '  - `seelegent doctor` checks the installation; `seelegent ui` opens the dashboard.'
    Write-Host '  - `seelegent update` checks for new releases later.'
    if ($failed.Count -gt 0) {
        throw "Seelegent $relTag is installed, but these steps failed: $($failed -join ', '). Run them again after fixing what they reported."
    }
}

Install-Seelegent @args
