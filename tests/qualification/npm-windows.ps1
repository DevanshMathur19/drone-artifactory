$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
Set-StrictMode -Version Latest

$candidate = $env:CANDIDATE_IMAGE
if ($candidate -notmatch '@(sha256:[0-9a-f]{64})$') {
  throw 'Node candidate must be digest pinned'
}
$expectedDigest = $Matches[1]
if ([System.Environment]::OSVersion.Version.Build.ToString() -ne $env:EXPECTED_WINDOWS_BUILD) {
  throw "Expected Windows build $env:EXPECTED_WINDOWS_BUILD"
}
foreach ($command in @('pwsh', 'jf', 'jfrog', 'node', 'npm', 'npx')) {
  if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
    throw "Required command is unavailable: $command"
  }
}
if ((node --version).Trim().TrimStart('v') -ne $env:EXPECTED_NODE_VERSION) {
  throw "Unexpected Node version: $(node --version)"
}
$alternates = (& { . C:\tools\npm\Use-Npm.ps1 -List } 2>&1 | Out-String)
foreach ($version in @('5.6.0','6.4.1','6.11.3','6.14.4','6.14.7','6.14.8')) {
  if ($alternates -notmatch [regex]::Escape($version)) { throw "Missing npm $version" }
}

$privateCaPath = 'C:\temp\qualification-private-ca.pem'
$rsa = [System.Security.Cryptography.RSA]::Create(2048)
try {
  $request = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
    'CN=Artifactory npm qualification private CA',
    $rsa,
    [System.Security.Cryptography.HashAlgorithmName]::SHA256,
    [System.Security.Cryptography.RSASignaturePadding]::Pkcs1
  )
  $request.CertificateExtensions.Add(
    [System.Security.Cryptography.X509Certificates.X509BasicConstraintsExtension]::new($true, $false, 0, $true)
  )
  $certificate = $request.CreateSelfSigned([DateTimeOffset]::UtcNow.AddMinutes(-5), [DateTimeOffset]::UtcNow.AddDays(1))
  try {
    $privateCaPem = "-----BEGIN CERTIFICATE-----`n$([Convert]::ToBase64String(
      $certificate.Export([System.Security.Cryptography.X509Certificates.X509ContentType]::Cert),
      [Base64FormattingOptions]::InsertLineBreaks
    ))`n-----END CERTIFICATE-----`n"
  } finally {
    $certificate.Dispose()
  }
} finally {
  $rsa.Dispose()
}

$tokenPath = 'C:\var\run\secrets\kubernetes.io\serviceaccount\token'
$caPath = 'C:\var\run\secrets\kubernetes.io\serviceaccount\ca.crt'
$token = Get-Content -LiteralPath $tokenPath -Raw
$namespace = $env:POD_NAMESPACE
$pod = if ($env:POD_NAME) { $env:POD_NAME } else { $env:COMPUTERNAME }
$base = "https://$($env:KUBERNETES_SERVICE_HOST):$($env:KUBERNETES_SERVICE_PORT_HTTPS)/api/v1/namespaces/$namespace/pods"
$curl = @('--fail','--silent','--show-error','--ssl-no-revoke','--cacert',$caPath,'-H',"Authorization: Bearer $token")
$json = & curl.exe @curl "$base/$pod"
if ($LASTEXITCODE -ne 0) {
  $listing = (& curl.exe @curl $base | ConvertFrom-Json).items
  $addresses = @(
    [System.Net.Dns]::GetHostAddresses([System.Net.Dns]::GetHostName()) |
      Where-Object AddressFamily -eq ([System.Net.Sockets.AddressFamily]::InterNetwork) |
      ForEach-Object IPAddressToString
  )
  $matches = @($listing | Where-Object { $addresses -contains [string] $_.status.podIP })
  if ($matches.Count -ne 1) { throw "Could not identify the current Pod in namespace $namespace" }
  $status = $matches[0].status
} else {
  $status = ($json | ConvertFrom-Json).status
}
$imageIds = @($status.containerStatuses | ForEach-Object { [string] $_.imageID })
if (@($imageIds | Where-Object { $_ -match "@$([regex]::Escape($expectedDigest))$" }).Count -lt 1) {
  throw "Expected running imageID $expectedDigest; observed: $($imageIds -join ', ')"
}

$artifactory = $env:JFROG_URL.TrimEnd('/')
$platform = $artifactory -replace '/artifactory$', ''
$hostName = ([uri]$platform).Host
$credentialBytes = [System.Text.Encoding]::UTF8.GetBytes("$($env:JFROG_USERNAME):$($env:JFROG_PASSWORD)")
$headers = @{Authorization = "Basic $([Convert]::ToBase64String($credentialBytes))"}
$repo = "$($env:NPM_REPOSITORY_PREFIX)-ltsc$($env:LTSC)"
$repoUri = "$artifactory/api/repositories/$repo"
$probe = Invoke-WebRequest -UseBasicParsing -SkipHttpErrorCheck -Headers $headers -Uri $repoUri
if ([int]$probe.StatusCode -eq 404) {
  $config = @{key=$repo; rclass='local'; packageType='npm'; repoLayoutRef='npm-default'} | ConvertTo-Json -Compress
  $created = Invoke-WebRequest -UseBasicParsing -SkipHttpErrorCheck -Method Put -Headers $headers -ContentType 'application/json' -Body $config -Uri $repoUri
  if ([int]$created.StatusCode -notin @(200,201)) {
    throw "Could not create npm sandbox repository $repo; HTTP $([int]$created.StatusCode): $($created.Content)"
  }
} elseif (-not $probe.IsSuccessStatusCode) {
  throw "Could not inspect npm sandbox repository $repo; HTTP $([int]$probe.StatusCode)"
}

$project = "fixture node$($env:NODE_MAJOR) ltsc$($env:LTSC)"
New-Item -ItemType Directory -Force $project | Out-Null
$packageName = "artifactory-node$($env:NODE_MAJOR)-ltsc$($env:LTSC)-$($env:PIPELINE_SEQUENCE_ID)"
$packageVersion = "1.0.$($env:PIPELINE_SEQUENCE_ID)"
@{name=$packageName; version=$packageVersion; description='Artifactory npm qualification'; license='UNLICENSED'} |
  ConvertTo-Json | Set-Content -LiteralPath "$project\package.json" -Encoding utf8
@{name=$packageName; version=$packageVersion; lockfileVersion=1; requires=$true; dependencies=@{}} |
  ConvertTo-Json -Depth 4 | Set-Content -LiteralPath "$project\package-lock.json" -Encoding utf8

function Invoke-Publisher {
  param(
    [string]$Operation,
    [string]$URL,
    [string]$AuthMode,
    [string]$NpmVersion,
    [bool]$ExpectSuccess
  )
  $buildName = "artifactory-node$($env:NODE_MAJOR)-ltsc$($env:LTSC)-$Operation"
  $env:PLUGIN_BUILD_TOOL = 'npm'
  $env:PLUGIN_COMMAND = $Operation
  $env:PLUGIN_URL = $URL
  $env:PLUGIN_PROJECT_DIR = $project
  $env:PLUGIN_REPO_RESOLVE = $repo
  $env:PLUGIN_REPO_DEPLOY = $repo
  $env:PLUGIN_RESOLVER_ID = "npm-resolve-$($env:NODE_MAJOR)-$($env:LTSC)-$Operation"
  $env:PLUGIN_DEPLOYER_ID = "npm-deploy-$($env:NODE_MAJOR)-$($env:LTSC)-$Operation"
  $env:PLUGIN_BUILD_NAME = $buildName
  $env:PLUGIN_BUILD_NUMBER = $env:PIPELINE_SEQUENCE_ID
  $env:PLUGIN_MODULE = $packageName
  $env:PLUGIN_PUBLISH_BUILD_INFO = 'true'
  $env:PLUGIN_NPM_VERSION = $NpmVersion
  $env:PLUGIN_ENABLE_PROXY = 'true'
  $env:PLUGIN_PEM_FILE_CONTENTS = $privateCaPem
  $env:PLUGIN_PEM_FILE_PATH = $privateCaPath
  $env:HARNESS_HTTPS_PROXY = 'http://127.0.0.1:9'
  $env:HARNESS_NO_PROXY = $hostName
  if ($AuthMode -eq 'token') {
    Remove-Item Env:PLUGIN_USERNAME,Env:PLUGIN_PASSWORD -ErrorAction SilentlyContinue
    $env:PLUGIN_ACCESS_TOKEN = $env:JFROG_PASSWORD
  } else {
    Remove-Item Env:PLUGIN_ACCESS_TOKEN -ErrorAction SilentlyContinue
    $env:PLUGIN_USERNAME = $env:JFROG_USERNAME
    $env:PLUGIN_PASSWORD = if ($AuthMode -eq 'invalid') { 'known-invalid-password' } else { $env:JFROG_PASSWORD }
  }
  $output = (& 'C:\bin\drone-artifactory.exe' 2>&1 | Out-String)
  $exitCode = $LASTEXITCODE
  if ($output -match [regex]::Escape($env:JFROG_PASSWORD)) { throw 'Plugin output exposed the credential' }
  Write-Output $output
  if ($ExpectSuccess -and $exitCode -ne 0) { throw "$Operation failed with exit code $exitCode" }
  if (-not $ExpectSuccess -and $exitCode -eq 0) { throw "$Operation unexpectedly succeeded with invalid credentials" }
  if ($ExpectSuccess) {
    $encodedName = [uri]::EscapeDataString($buildName)
    $build = Invoke-WebRequest -UseBasicParsing -SkipHttpErrorCheck -Headers $headers -Uri "$artifactory/api/build/$encodedName/$($env:PIPELINE_SEQUENCE_ID)"
    if (-not $build.IsSuccessStatusCode) { throw "Build info is absent for $buildName" }
  }
}

Invoke-Publisher -Operation install -URL $platform -AuthMode password -NpmVersion '' -ExpectSuccess $true
if (-not (Test-Path -LiteralPath $privateCaPath -PathType Leaf)) {
  throw 'Injected private CA was not materialized'
}
if ((Get-Content -LiteralPath $privateCaPath -Raw).Trim() -ne $privateCaPem.Trim()) {
  throw 'Materialized private CA does not match the injected PEM'
}
Invoke-Publisher -Operation ci -URL "$platform/artifactory/" -AuthMode token -NpmVersion '6.14.8' -ExpectSuccess $true
Invoke-Publisher -Operation publish -URL "$platform/artifactory/$repo" -AuthMode password -NpmVersion '6.14.8' -ExpectSuccess $true
Invoke-Publisher -Operation publish -URL $platform -AuthMode invalid -NpmVersion '6.14.8' -ExpectSuccess $false

Write-Output "Node $($env:EXPECTED_NODE_VERSION) LTSC $($env:LTSC) npm qualification passed for $candidate"
