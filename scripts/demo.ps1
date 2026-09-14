param([string]$BaseUrl='http://127.0.0.1:8082')
$ErrorActionPreference='Stop'
if ($env:VAULT_KEY.Length -lt 16) { throw 'Set VAULT_KEY in this terminal first.' }
$h=@{Authorization="Bearer $env:VAULT_KEY"}
$body=[System.Text.Encoding]::UTF8.GetBytes("same content $([guid]::NewGuid())")
$a=Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/files?name=first.txt" -Headers $h -ContentType 'application/octet-stream' -Body $body
$b=Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/files?name=second.txt" -Headers $h -ContentType 'application/octet-stream' -Body $body
if (-not $b.deduplicated -or $a.file.sha256 -ne $b.file.sha256) { throw 'Dedup failed.' }
$config=@{downloads=1;ttl_seconds=60}|ConvertTo-Json
$share=Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/files/$($a.file.id)/shares" -Headers $h -ContentType 'application/json' -Body $config
$url="$BaseUrl/s/$($share.token)"
$first=Invoke-WebRequest -UseBasicParsing -Uri $url
if ($first.StatusCode -ne 200) { throw 'First download failed.' }
$gone=$false
try { $null=Invoke-WebRequest -UseBasicParsing -Uri $url }
catch { if ([int]$_.Exception.Response.StatusCode -eq 410) { $gone=$true } else { throw } }
if (-not $gone) { throw 'Second download must return 410.' }
Invoke-RestMethod -Uri "$BaseUrl/api/stats" -Headers $h | ConvertTo-Json
Write-Host 'PASS: same content deduplicated; one-shot ticket spent exactly once.' -ForegroundColor Green
