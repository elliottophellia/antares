// Program catalogues for each hook category. The descriptions are the ones
// the model sees in the tool schema, so they are kept terse and operational:
// what the program does, what it expects, and any post-condition (cleanup
// before leaving, root required, and so on).
//
// Sources: the original TypeScript wrappers in
// packages/cyberstrike/src/tool/{attack-script,awshook,azurehook,kubehook,
// winhook,machook,cipipe,ebpf}.ts. Keep these in sync if a script is added
// or its CLI changes.

package hookpack

var attackScripts = []Program{
	{Name: "jwt_tamper", Description: "JWT token decode/modify/re-encode for auth bypass testing (alg=none, role escalation, user ID swap)", Args: "TOKEN [--decode-only] [--set key=value] [--set-header key=value] [--key SECRET] [--json-output]"},
	{Name: "race_tester", Description: "Race condition tester — concurrent requests to detect TOCTOU vulnerabilities", Args: "URL [-m METHOD] [-H key:value] [-d JSON_BODY] [-c COUNT] [--delay MS] [--json-output]"},
	{Name: "ssti_tester", Description: "Server-Side Template Injection detection and engine fingerprinting (Jinja2, FreeMarker, Velocity, Twig, ERB, Pebble)", Args: "URL [--param NAME] [--method GET|POST] [--data JSON] [-H key:value] [--quick] [--json-output]"},
	{Name: "ssrf_listener", Description: "SSRF callback listener — lightweight HTTP server that logs all incoming requests as evidence", Args: "[-p PORT] [-o OUTPUT_FILE] [--timeout SECONDS]"},
	{Name: "idor_tester", Description: "IDOR cross-account access tester with two sets of credentials", Args: "--token-a TOKEN --token-b TOKEN --endpoints FILE_OR_CSV [--method METHOD] [--data JSON] [--json-output]"},
	{Name: "cors_checker", Description: "CORS misconfiguration checker — tests origin reflection, wildcard, null origin, bypass patterns", Args: "URL [--json-output]"},
	{Name: "graphql_tester", Description: "GraphQL vulnerability tester — introspection, complexity DoS, batch abuse, alias DoS", Args: "URL [-H key:value] [--depth N] [--batch-count N] [--json-output]"},
	{Name: "file_upload_tester", Description: "File upload vulnerability tester — extension bypass, MIME bypass, polyglot files, SVG XSS/SSRF", Args: "URL [--field NAME] [-H key:value] [--data JSON] [--json-output]"},
	{Name: "oauth_tester", Description: "OAuth 2.0 vulnerability tester — redirect_uri bypass, state manipulation, scope escalation", Args: "AUTH_URL --client-id ID --redirect-uri URI [--json-output]"},
	{Name: "rate_limit_bypass", Description: "Rate limit bypass tester — XFF rotation, case variation, method switching, query params, header variations", Args: "URL [--method METHOD] [-H key:value] [-d JSON] [--count N] [--json-output]"},
	{Name: "waf_bypass", Description: "WAF bypass encoder — generates encoding variants (URL, unicode, HTML entity, hex, mixed case) with optional live testing", Args: "PAYLOAD [--test-url URL] [--param NAME] [--json-output]"},
	{Name: "cloud_storage_enum", Description: "Cloud storage enumeration — S3/Azure/GCP bucket discovery with permission checks", Args: "TARGET [--names BUCKET_NAME...] [--json-output]"},
	{Name: "subdomain_takeover", Description: "Subdomain takeover checker — CNAME detection + cloud service fingerprint matching (20 services)", Args: "FILE_OR_DOMAIN [--json-output]"},
	{Name: "github_dorker", Description: "GitHub intelligence dorker — search for leaked secrets using gh CLI (30+ patterns)", Args: "ORG [--repo ORG/REPO] [--patterns PATTERN...] [--commits] [--json-output]"},
	{Name: "wayback_endpoints", Description: "Wayback Machine endpoint discovery — find historical/hidden endpoints via CDX API", Args: "DOMAIN [--probe] [--limit N] [--json-output]"},
	{Name: "response_diff", Description: "HTTP response diff — compare two responses with different headers to detect access control differences", Args: "URL [--header-a key:value] [--header-b key:value] [--method METHOD] [--data JSON] [--json-output]"},
}

var awsPrograms = []Program{
	{Name: "iam_enum", Description: "Enumerate IAM users, roles, policies, and analyze for privilege escalation paths (PassRole, wildcard policies, inline policy abuse)", Args: "[--profile PROFILE] [--region REGION] [--json-output]"},
	{Name: "iam_privesc", Description: "Exploit IAM misconfigurations for privilege escalation: PassRole+Lambda, AssumeRole chaining, AttachUserPolicy, CreateLoginProfile, CreateAccessKey", Args: "--method <passrole|assumerole|lambda|attach_policy|create_login|create_key> [--role-arn ARN] [--profile PROFILE] [--json-output]"},
	{Name: "s3_dump", Description: "List all S3 buckets, identify sensitive files (.env, backups, credentials, .pem, .key), and optionally download high-value targets", Args: "[--bucket BUCKET] [--download] [--pattern REGEX] [--profile PROFILE] [--json-output]"},
	{Name: "lambda_backdoor", Description: "Inject reverse shell layer into existing Lambda function or create new backdoor function with high-privilege role", Args: "--function-name NAME --callback-url URL [--method inject|create] [--profile PROFILE] [--json-output]"},
	{Name: "ssm_exec", Description: "Execute commands on EC2 instances via AWS Systems Manager RunCommand — no SSH or direct network access required", Args: "--instance-id ID --command CMD [--all-instances] [--profile PROFILE] [--json-output]"},
	{Name: "metadata_harvest", Description: "Extract IAM role credentials from EC2/ECS/Lambda metadata endpoints (169.254.169.254). Supports IMDSv1 and IMDSv2", Args: "[--imds-version v1|v2] [--json-output]"},
	{Name: "cloudtrail_blind", Description: "Stop CloudTrail logging, manipulate event selectors to exclude management events, or delete existing log files from S3", Args: "--action <stop|delete_logs|modify_selectors|status> [--trail-name NAME] [--profile PROFILE] [--json-output]"},
	{Name: "secrets_dump", Description: "Extract all secrets from AWS Secrets Manager and SSM Parameter Store (SecureString parameters with decryption)", Args: "[--service secretsmanager|ssm|all] [--profile PROFILE] [--region REGION] [--json-output]"},
	{Name: "ec2_snapshot", Description: "Create EBS volume snapshots for data exfiltration, optionally share cross-account for offline analysis", Args: "--volume-id VOL_ID [--share-account ACCOUNT_ID] [--profile PROFILE] [--json-output]"},
	{Name: "cleanup_aws", Description: "Remove all CyberStrike-created AWS resources, restore CloudTrail logging, clean state files. ALWAYS run before leaving", Args: "[--dry-run] [--profile PROFILE] [--json-output]"},
}

var azurePrograms = []Program{
	{Name: "entra_enum", Description: "Enumerate Entra ID (Azure AD) users, groups, app registrations, service principals, and conditional access policies via Microsoft Graph API", Args: "[--tenant-id TENANT] [--json-output]"},
	{Name: "entra_privesc", Description: "Exploit Entra ID misconfigurations for privilege escalation: illicit consent grant, Global Admin via PIM, service principal secret injection", Args: "--method <consent_grant|pim_activate|sp_secret|app_role> [--target-id ID] [--json-output]"},
	{Name: "keyvault_dump", Description: "Extract secrets, keys, and certificates from all accessible Azure Key Vaults in the subscription", Args: "[--vault-name NAME] [--subscription-id SUB] [--json-output]"},
	{Name: "storage_dump", Description: "Enumerate and download sensitive data from Azure Blob Storage containers, Tables, and Queues", Args: "[--account-name NAME] [--container CONTAINER] [--download] [--pattern REGEX] [--json-output]"},
	{Name: "managed_identity", Description: "Extract managed identity OAuth tokens from Azure VM, App Service, Functions, or Container Instances via IMDS endpoint", Args: "[--resource RESOURCE_URL] [--json-output]"},
	{Name: "runbook_backdoor", Description: "Create or modify Azure Automation Account runbook with reverse shell or credential harvesting PowerShell, then publish and schedule", Args: "--automation-account NAME --resource-group RG [--callback-url URL] [--json-output]"},
	{Name: "azuread_token", Description: "Manipulate Azure AD tokens: refresh token exchange for new scopes, PRT extraction from device state, FOCI (Family of Client IDs) abuse", Args: "--action <refresh|prt|foci> [--refresh-token TOKEN] [--client-id ID] [--json-output]"},
	{Name: "cleanup_azure", Description: "Remove all CyberStrike-created Azure resources, delete added SP secrets, remove runbooks, clean state files. ALWAYS run before leaving", Args: "[--dry-run] [--json-output]"},
}

var kubePrograms = []Program{
	{Name: "k8s_enum", Description: "Enumerate Kubernetes cluster: namespaces, pods, services, secrets (metadata), RBAC roles/bindings, ingress, and service accounts", Args: "[--namespace NS] [--kubeconfig PATH] [--json-output]"},
	{Name: "k8s_secrets", Description: "Extract and base64-decode Kubernetes Secrets from all accessible namespaces. Filters by type (Opaque, TLS, docker-registry)", Args: "[--namespace NS] [--type TYPE] [--kubeconfig PATH] [--json-output]"},
	{Name: "k8s_escape", Description: "Detect and exploit container escape vectors: privileged mode, hostPID/hostNetwork, writable hostPath, mounted docker socket, SYS_ADMIN capability", Args: "[--exploit] [--json-output]"},
	{Name: "k8s_privesc", Description: "Kubernetes RBAC privilege escalation: steal ServiceAccount tokens, create ClusterRoleBinding for cluster-admin, abuse token request API", Args: "--method <sa_token|bind_admin|token_request> [--namespace NS] [--sa-name NAME] [--kubeconfig PATH] [--json-output]"},
	{Name: "etcd_dump", Description: "Connect directly to etcd and extract all Kubernetes secrets from /registry/secrets/ prefix. Requires etcd credentials or certs", Args: "--endpoint ENDPOINT [--cert CERT] [--key KEY] [--ca CA] [--json-output]"},
	{Name: "k8s_backdoor", Description: "Deploy persistent backdoor via DaemonSet (runs on every node) or CronJob (periodic callback) with configurable image and callback URL", Args: "--type <daemonset|cronjob> --image IMAGE [--callback-url URL] [--namespace NS] [--kubeconfig PATH] [--json-output]"},
	{Name: "cleanup_k8s", Description: "Remove all CyberStrike-created Kubernetes resources (by label app=cyberstrike): DaemonSets, CronJobs, ClusterRoleBindings, Pods. ALWAYS run before leaving", Args: "[--kubeconfig PATH] [--json-output]"},
}

var winPrograms = []Program{
	{Name: "lsass_dump", Description: "Dump LSASS process memory for credential extraction using MiniDumpWriteDump or comsvcs.dll — extracts NTLM hashes, Kerberos tickets, and plaintext passwords", Args: "[--method comsvcs|minidump] [--outfile PATH] [--json-output]"},
	{Name: "sam_dump", Description: "Extract SAM, SYSTEM, and SECURITY registry hives for offline password cracking with secretsdump or hashcat", Args: "[--outdir PATH] [--json-output]"},
	{Name: "dpapi_extract", Description: "Decrypt DPAPI-protected secrets — Chrome/Edge saved passwords, WiFi keys, Windows Credential Vault, and application credentials", Args: "[--scope user|machine] [--browser chrome|edge|all] [--json-output]"},
	{Name: "credential_prompt", Description: "Spawn a fake Windows credential dialog via CredUIPromptForCredentials to phish the current user's password", Args: "[--message TEXT] [--title TEXT] [--json-output]"},
	{Name: "keylog_win", Description: "Capture keystrokes via SetWindowsHookEx with WH_KEYBOARD_LL — logs keystrokes with window title context", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "etw_process", Description: "Monitor process creation and termination via ETW Microsoft-Windows-Kernel-Process provider — capture PID, PPID, image path, command line", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "etw_network", Description: "Monitor network connections via ETW Microsoft-Windows-Kernel-Network provider — capture source/dest IP, port, PID, protocol", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "clipboard_sniff", Description: "Monitor clipboard contents for passwords, API tokens, and sensitive data copied by users — polls at configurable interval", Args: "[--duration SECONDS] [--interval SECONDS] [--json-output]"},
	{Name: "amsi_bypass", Description: "Bypass AMSI (Antimalware Scan Interface) by patching AmsiScanBuffer in memory — enables undetected PowerShell script execution", Args: "[--method patch|reflection|clr] [--json-output]"},
	{Name: "etw_blind", Description: "Patch NtTraceEvent / EtwEventWrite in ntdll.dll to blind EDR and AV monitoring in the current process", Args: "[--json-output]"},
	{Name: "defender_exclude", Description: "Add exclusion paths to Windows Defender via PowerShell to prevent scanning of CyberStrike tools and payloads", Args: "--path PATH [--json-output]"},
	{Name: "cleanup_win", Description: "Remove CyberStrike artifacts — clear Security/System/Application event logs, remove scheduled tasks, restore AMSI/ETW patches. ALWAYS run before leaving a target", Args: "[--json-output]"},
}

var macPrograms = []Program{
	{Name: "keychain_dump", Description: "Extract passwords from macOS Keychain via security command and Keychain API — dumps login, system, and application keychains", Args: "[--keychain PATH] [--json-output]"},
	{Name: "chrome_creds", Description: "Extract Chrome and Safari saved passwords, cookies, and autofill data from local browser storage — decrypts via Safe Storage key", Args: "[--browser chrome|safari|all] [--json-output]"},
	{Name: "ssh_keys", Description: "Find and exfiltrate SSH private keys, known_hosts, authorized_keys, and SSH agent identities for all users", Args: "[--user USER] [--json-output]"},
	{Name: "tcc_bypass", Description: "Bypass Transparency, Consent, and Control (TCC) framework to access protected resources — camera, microphone, files, screen recording", Args: "[--method direct|inject|reset] [--json-output]"},
	{Name: "keylog_mac", Description: "Capture keystrokes via CGEventTap or IOKit HID API — logs key events with application context and window title", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "dtrace_exec", Description: "Monitor all process executions system-wide via DTrace syscall::exec*: probes — capture PID, PPID, command, arguments (requires SIP disabled)", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "dtrace_net", Description: "Monitor network connections via DTrace ip:::send and ip:::receive probes — capture source/dest IP, port, PID, bytes", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "dtrace_file", Description: "Monitor file access via DTrace syscall::open*: probes — capture PID, process name, file path, flags", Args: "[--duration SECONDS] [--pid PID] [--json-output]"},
	{Name: "xprotect_check", Description: "Enumerate XProtect and MRT (Malware Removal Tool) signatures to identify what payloads and techniques would be detected", Args: "[--json-output]"},
	{Name: "gatekeeper_bypass", Description: "Remove com.apple.quarantine extended attribute from downloaded files to bypass Gatekeeper code signing checks", Args: "--path PATH [--recursive] [--json-output]"},
	{Name: "log_clear", Description: "Clear unified logging (ASL), audit logs at /var/audit/, system log archives, and crash reporter data", Args: "[--json-output]"},
	{Name: "cleanup_mac", Description: "Remove CyberStrike artifacts — LaunchAgents, DTrace scripts, log modifications, temporary files. ALWAYS run before leaving a target", Args: "[--json-output]"},
}

var ciPrograms = []Program{
	{Name: "gh_secrets", Description: "Extract GitHub Actions secrets via workflow injection or log analysis. Creates dispatch workflow that exfiltrates secrets to controlled endpoint", Args: "--repo OWNER/REPO [--token TOKEN] [--method dispatch|logs] [--json-output]"},
	{Name: "jenkins_creds", Description: "Dump Jenkins credentials: access credentials.xml via API, execute Groovy scripts via Script Console, extract build environment variables", Args: "--url JENKINS_URL [--username USER] [--token TOKEN] [--method api|console|env] [--json-output]"},
	{Name: "pipeline_inject", Description: "Inject malicious steps into CI/CD pipeline configurations (.github/workflows, Jenkinsfile, .gitlab-ci.yml) via API", Args: "--repo OWNER/REPO --callback-url URL [--platform github|gitlab] [--token TOKEN] [--json-output]"},
	{Name: "gitlab_tokens", Description: "Extract GitLab CI/CD variables (project and group level), runner registration tokens, and personal access tokens via GitLab API", Args: "--url GITLAB_URL --project-id ID [--token TOKEN] [--json-output]"},
	{Name: "cleanup_ci", Description: "Remove injected pipeline modifications, close created PRs, delete branches, and revert workflow changes. ALWAYS run before leaving", Args: "[--token TOKEN] [--json-output]"},
}

var ebpfPrograms = []Program{
	{Name: "pam_sniff", Description: "Hook pam_get_authtok via eBPF uprobe — capture cleartext passwords for every PAM-authenticated session (SSH, sudo, su, login, screen lock)", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "ssl_sniff", Description: "Hook SSL_write/SSL_read via eBPF uprobe on libssl.so — capture TLS plaintext before encryption and after decryption", Args: "[--pid PID] [--duration SECONDS] [--json-output]"},
	{Name: "dep_scan", Description: "Scan all running processes for loaded shared libraries via sys_openat kprobe, report library paths and identify potentially vulnerable dependencies", Args: "[--pid PID] [--json-output]"},
	{Name: "proc_hide", Description: "Hide a process from ps, top, htop, and /proc enumeration by hooking sys_getdents64 on /proc", Args: "--pid PID [--json-output]"},
	{Name: "file_hide", Description: "Hide files or directories from ls, find, and directory listings by hooking sys_getdents64", Args: "--name FILENAME [--json-output]"},
	{Name: "conn_hide", Description: "Hide network connections from netstat, ss, and /proc/net/tcp by hooking sys_read on procfs", Args: "--port PORT [--json-output]"},
	{Name: "execve_sniff", Description: "Monitor all process executions system-wide via sys_execve tracepoint — capture PID, PPID, command, and arguments", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "dns_sniff", Description: "Capture DNS queries at the kernel level by hooking udp_sendmsg on port 53 — extract query name, type, and destination", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "keylog", Description: "Capture keystrokes from TTY file descriptors by hooking sys_read on /dev/tty — outputs PID, process name, and captured input", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "cleanup", Description: "Enumerate and detach all CyberStrike eBPF programs and maps from the system. Always run this before exiting a target", Args: "[--json-output]"},
	{Name: "io_uring_sniff", Description: "Monitor io_uring ring buffer operations — detect file, socket, and connect operations that bypass classical syscall hooks via submission queue entries (kernel 5.1+)", Args: "[--duration SECONDS] [--pid PID] [--json-output] [--dangerous-only]"},
	{Name: "memfd_exec", Description: "Detect fileless execution via memfd_create + execveat — correlate memory-only file creation with execution to identify diskless payload delivery", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "ptrace_sniff", Description: "Monitor ptrace-based process injection — capture ATTACH, POKEDATA, SETREGS operations and detect shellcode injection sequences", Args: "[--duration SECONDS] [--pid PID] [--json-output]"},
	{Name: "crossmem_sniff", Description: "Monitor cross-process memory operations via process_vm_writev/readv — detect stealthy memory injection that bypasses ptrace-based detection", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "userfaultfd_sniff", Description: "Monitor userfaultfd creation and page fault handling — detect race condition exploit primitives that use userspace page fault handlers for timing control", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "bpf_integrity", Description: "Monitor bpf() syscall for program load/attach/detach operations and verify integrity of CyberStrike eBPF programs — detect tampering, unauthorized BPF program injection, and hook evasion", Args: "[--duration SECONDS] [--json-output] [--baseline] [--check-interval SECONDS]"},
	{Name: "netlink_sniff", Description: "Monitor netlink socket messages for stealthy network configuration changes — detect route manipulation, firewall rule injection, and policy routing modifications", Args: "[--duration SECONDS] [--json-output] [--route-only]"},
	{Name: "seccomp_sniff", Description: "Monitor prctl and seccomp syscalls for security profile self-modification — detect processes weakening their own sandboxes, changing names for masquerading, or disabling privilege restrictions", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "mmap_sniff", Description: "Monitor shared memory creation via mmap MAP_SHARED, shmget, and shmat — detect covert IPC channels where data flows in memory without generating syscalls after initial mapping", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "zerocopy_sniff", Description: "Monitor zero-copy data transfers via splice, tee, and sendfile64 — detect fd-to-fd data movement invisible to userspace profilers and buffer-based monitoring", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "vdso_sniff", Description: "Monitor VDSO timing calls (clock_gettime, gettimeofday) and mprotect on high-address VDSO pages — detect timing side-channels and VDSO page tampering", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "keyring_sniff", Description: "Monitor kernel keyring operations (add_key, keyctl, request_key) — detect credential storage in kernel keyring to evade filesystem monitoring", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "namespace_sniff", Description: "Monitor namespace changes via setns and unshare — detect container escapes and namespace pivoting that makes processes invisible to single-namespace monitoring", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "ioctl_sniff", Description: "Monitor dangerous ioctl operations (TIOCSTI terminal keystroke injection, TIOCLINUX, TIOCSCTTY controlling terminal steal) — detect terminal manipulation attacks", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "mount_sniff", Description: "Monitor mount/umount operations for overlay mounts, bind mounts over sensitive paths (/etc, /usr, /bin), and FUSE filesystem manipulation used to hide changes", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "fuse_sniff", Description: "Monitor FUSE filesystem operations — detect /dev/fuse opens and fuse-type mounts where file operations bypass kernel VFS and run in attacker-controlled userspace code", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "perf_sniff", Description: "Monitor perf_event_open syscall — detect side-channel attacks abusing hardware performance counters (cache misses, branch mispredictions) for information leakage", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "bpfmap_sniff", Description: "Monitor BPF map operations (create, lookup, update, delete) — detect covert channels using BPF maps for inter-process data sharing and exfiltration", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "ldpreload_sniff", Description: "Monitor LD_PRELOAD environment variable injection and dynamic linker configuration changes (/etc/ld.so.preload, /etc/ld.so.conf) — detect library injection before process start", Args: "[--duration SECONDS] [--json-output]"},
	{Name: "futex_sniff", Description: "Monitor futex WAIT/WAKE operations — detect timing-based covert channels using futex signaling between processes and busy-wait exploitation loops", Args: "[--duration SECONDS] [--json-output]"},
}
