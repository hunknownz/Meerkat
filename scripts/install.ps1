param([switch]$NoHost,[switch]$NoExecutor,[switch]$NoStart,[switch]$Source,[string]$DataDir,[string]$RuntimeDir,[string]$ArtifactDir)
$ErrorActionPreference='Stop'
# Native Windows bootstrap. Installs missing toolchains privately, not system
# services, WSL or global npm packages. No provider/model request is made.
$root=Split-Path $PSScriptRoot -Parent
$tools=Join-Path $env:USERPROFILE '.meerkat\tools'
function Private-Dir([string]$p) {
  if(-not (Test-Path -LiteralPath $p)) {
    $parent=Split-Path $p -Parent
    if(-not (Test-Path -LiteralPath $parent)){Private-Dir $parent}
    [void](New-Item -ItemType Directory -Path $p)
    & "$PSScriptRoot\lib\private.ps1" -Path $p -Action protect
  }
  & "$PSScriptRoot\lib\private.ps1" -Path $p -Action check
}
function Download-Verified([string]$url,[string]$file,[string]$hash) {
  Invoke-WebRequest -Uri $url -OutFile $file -UseBasicParsing
  if((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() -ne $hash.ToLowerInvariant()){Remove-Item -LiteralPath $file;throw 'Toolchain checksum mismatch'}
}
$node=Get-Command node -ErrorAction SilentlyContinue
$nodeOK=$false
if($node){$v=(& $node.Source -p 'process.versions.node').Split('.');$nodeOK=([int]$v[0] -gt 22 -or ([int]$v[0] -eq 22 -and [int]$v[1] -ge 19))}
if(-not $nodeOK){
  Private-Dir $tools
  $arch=if([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64'){'arm64'}else{'x64'}
  $releases=Invoke-RestMethod 'https://nodejs.org/dist/index.json'
  $release=$releases | Where-Object {$_.version -match '^v22\.' -and $_.files -contains "win-$arch-zip"} | Sort-Object {[version]$_.version.Substring(1)} -Descending | Select-Object -First 1
  if(-not $release){throw 'No supported Node 22 Windows release'}
  $name="node-$($release.version)-win-$arch.zip"
  $sums=Invoke-WebRequest "https://nodejs.org/dist/$($release.version)/SHASUMS256.txt" -UseBasicParsing
  $match=[regex]::Match($sums.Content,"(?m)^([a-f0-9]{64})\s+$([regex]::Escape($name))\r?$")
  if(-not $match.Success){throw 'Node checksum unavailable'}
  $archive=Join-Path $tools $name; Download-Verified "https://nodejs.org/dist/$($release.version)/$name" $archive $match.Groups[1].Value
  Expand-Archive -LiteralPath $archive -DestinationPath $tools -Force;Remove-Item -LiteralPath $archive
  $nodePath=Join-Path $tools "$($name.Substring(0,$name.Length-4))\node.exe"
}else{$nodePath=$node.Source}
if(-not (Get-Command go -ErrorAction SilentlyContinue)){
  Private-Dir $tools
  $arch=if([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64'){'arm64'}else{'amd64'}
  $release=(Invoke-RestMethod 'https://go.dev/dl/?mode=json&include=all' | Where-Object {$_.version -eq 'go1.26.0'} | Select-Object -First 1)
  $file=$release.files | Where-Object {$_.os -eq 'windows' -and $_.arch -eq $arch -and $_.kind -eq 'archive'} | Select-Object -First 1
  if(-not $file){throw 'Go 1.26.0 download unavailable'}
  $archive=Join-Path $tools $file.filename; Download-Verified "https://go.dev/dl/$($file.filename)" $archive $file.sha256
  $goDir=Join-Path $tools 'go1.26.0';Private-Dir $goDir
  Expand-Archive -LiteralPath $archive -DestinationPath $goDir -Force;Remove-Item -LiteralPath $archive
  $env:PATH="$(Join-Path $goDir 'go\bin');$env:PATH"
}
$env:PATH="$(Split-Path $nodePath -Parent);$env:PATH"
$arguments=@("$PSScriptRoot\install.mjs")
if($NoHost){$arguments+='--no-host'};if($NoExecutor){$arguments+='--no-executor'};if($NoStart){$arguments+='--no-start'};if($Source){$arguments+='--source'}
if($DataDir){$arguments+=@('--data-dir',$DataDir)};if($RuntimeDir){$arguments+=@('--runtime-dir',$RuntimeDir)};if($ArtifactDir){$arguments+=@('--artifact-dir',$ArtifactDir)}
& $nodePath @arguments
exit $LASTEXITCODE
