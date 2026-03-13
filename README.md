# 🚀 GoTunnel Pro: Self-Hosted Tunneling & Monetization Engine

A lightweight, zero-dependency tunneling server written in Go. It allows users to securely expose their local web servers, Minecraft servers, and SSH connections to the public internet, completely bypassing NAT and local firewalls.

Designed for SaaS operators, it includes a built-in Admin UI and a manual subscription management system (ideal for UPI or Bank Transfers).

## ✨ Features
* **Multi-Protocol Routing:** Supports HTTP/HTTPS reverse proxying and raw TCP forwarding.
* **Custom Subdomains:** Users get dedicated subdomains (e.g., `app.yourdomain.com`).
* **Cross-Platform Client:** Single-binary client for Windows, macOS, and Linux.
* **Subscription Enforcer:** Automatically disconnects users when their manual subscription expires.
* **Zero Dependencies:** Powered by an embedded SQLite database.

---

## 🛠️ 1. Server Setup (Admin Guide)

### Prerequisites
* A Linux VPS (Ubuntu/Debian recommended)
* Ports `8080`, `7000`, and `3050` open.
* A wildcard DNS record pointed to your VPS (e.g., `*.tun.yourdomain.com`).

### Deployment
1. Clone this repository to your VPS.
2. Install Go (1.18+).
3. Run the application:
   ```bash
   go run .
   
4. Configure your reverse proxy (Apache/Nginx) to route traffic to the internal ports:

    Admin UI: Port 3050

    HTTP Tunnels: Port 8080

    Note: Port 7000 must be exposed directly to the internet for the multiplexer.


📖 2. Client Usage (User Manual)

Once you have purchased a subscription and received your Token, follow these steps to expose your local applications.
1. Download the Client

Download the client for your Operating System from the Admin portal:
https://admin.yourdomain.com/downloads/
2. Run the Tunnel

Open your terminal or command prompt and run the software, passing your token and the local port you want to expose.

For Web Apps (HTTP/HTTPS):
If you have a local React or Python server running on port 8000:
./tunnel-client --token YOUR_16_CHAR_TOKEN --local 8000

Your app is now securely live at https://your-subdomain.yourdomain.com

For TCP Applications (Minecraft / SSH):
If your admin assigned you public TCP Port 20000, and you want to expose a local Minecraft server running on port 25565:
./tunnel-client --token YOUR_16_CHAR_TOKEN --local 25565

Players can now connect using yourdomain.com:20000