param([Parameter(Mandatory=$true)][string]$Path, [ValidateSet('check','protect')][string]$Action='check')
$ErrorActionPreference='Stop'
$item=Get-Item -LiteralPath $Path -Force
if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Reparse points are refused' }
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$sid=$identity.User
$acl=Get-Acl -LiteralPath $Path
$owner=$acl.GetOwner([Security.Principal.SecurityIdentifier]).Value
# protect is called only for a newly created object. Elevated tokens can give
# those objects the token's default owner; normalize it without adopting any
# pre-existing object in check mode, or an unrelated owner's object.
if ($owner -ne $sid.Value -and ($Action -ne 'protect' -or $owner -ne $identity.Owner.Value)) { throw 'Private path is not owned by the current user' }
if ($Action -eq 'protect') {
  $acl.SetOwner($sid)
  $acl.SetAccessRuleProtection($true,$false)
  foreach ($r in @($acl.Access)) { [void]$acl.RemoveAccessRuleSpecific($r) }
  $inherit=if($item.PSIsContainer){[Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{[Security.AccessControl.InheritanceFlags]::None}
  foreach($identity in @($sid,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'))) {
    $rule=[Security.AccessControl.FileSystemAccessRule]::new($identity,[Security.AccessControl.FileSystemRights]::FullControl,$inherit,[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AccessControlType]::Allow)
    $acl.AddAccessRule($rule)
  }
  Set-Acl -LiteralPath $Path -AclObject $acl
  $acl=Get-Acl -LiteralPath $Path
}
$rules=$acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])
if ($rules.Count -eq 0) { throw 'Private path has no access rules' }
foreach($r in $rules) {
  if ($r.AccessControlType -eq 'Allow' -and $r.IdentityReference.Value -notin @($sid.Value,'S-1-5-18')) { throw 'Private path grants another identity access' }
}
