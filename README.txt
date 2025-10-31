iFacialMocap UDP Relay - Usage & Troubleshooting Guide
======================================================

PURPOSE
-------
This Go binary acts as a lightweight UDP relay that receives ARKit-style tracking 
data from the iFacialMocap iOS app and forwards it simultaneously to multiple 
destinations (e.g., VTube Studio and Warudo) without introducing measurable latency.

The relay supports two output formats:
- RAW: Forwards original iFacialMocap JSON unchanged (for VTube Studio/VBridger)
- VSF: Converts to Virtual Stream Format JSON (for Warudo/VSeeFace/VMC)

INSTALLATION & BUILD
--------------------
1. Install Go 1.22 or later:
   winget install GoLang.Go

2. Build the binary:
   go build -ldflags "-s -w" -o ifmrelay.exe main.go

   For smallest binary size:
   go build -ldflags "-s -w" -trimpath -o ifmrelay.exe main.go

3. Place ifmrelay.exe and relay_config.json in the same directory.

USAGE
-----
1. Edit relay_config.json to match your setup:
   - listen_port: The port iFacialMocap sends to (default: 50000)
   - targets: List of destinations (host, port, name, format)
     * format: "raw" for original iFacialMocap JSON (default)
     * format: "vsf" for VSF (Virtual Stream Format) JSON conversion
   - buffer_size: Buffer size in bytes (default: 4096)
   - log_level: debug, info, or error
   - stats_interval: Seconds between statistics reports

2. Run the relay:
   .\ifmrelay.exe -config relay_config.json

3. Configure iFacialMocap iOS app:
   - Set your PC's IP address
   - Set port to 50000 (or your configured listen_port)

4. Verify both VTube Studio and Warudo receive tracking data simultaneously.

VERIFICATION
------------
When started correctly, you should see:
  [INFO] Listening on :50000
  [INFO] Forwarding to 127.0.0.1:49983 (VTubeStudio)
  [INFO] Forwarding to 127.0.0.1:39539 (Warudo) (VSF format)
  [INFO] Relay started successfully

Every 10 seconds (default), statistics will be printed:
  [STATS] Uptime: 1m30s | Received: 450 | Forwarded: 900 | Dropped: 0 | VSF Converted: 450 | Avg Latency: 0.123 ms

TROUBLESHOOTING
---------------
Symptom: No packets arriving
  Cause: Wrong port or Windows Firewall blocking UDP
  Fix: 
    - Verify iFacialMocap target IP/port matches relay listen_port
    - Add firewall exception for UDP port 50000 (inbound)
    - Check Windows Defender Firewall settings

Symptom: Only one app updates
  Cause: One target port is incorrect
  Fix:
    - Verify Warudo/VTS ports in relay_config.json match actual ports
    - Check if apps are listening on expected ports
    - Use "netstat -an | findstr :49983" to verify ports

Symptom: Intermittent lag
  Cause: OBS GPU saturation affecting system performance
  Fix:
    - Limit OBS NVENC preset to Quality (P5) to reduce encoder latency
    - Lower OBS canvas resolution if needed
    - Check GPU usage during streaming

Symptom: Dropped packets logged
  Cause: Network congestion or buffer overflow
  Fix:
    - Use wired Ethernet connection (avoid WiFi)
    - Increase buffer_size in config if using custom payloads
    - Ensure relay has adequate system resources

PERFORMANCE TARGETS
-------------------
- CPU usage: <0.5%
- Memory usage: <15 MB
- Average latency: <1 ms
- Packet loss: <0.1%

RUNNING AS BACKGROUND SERVICE
------------------------------
To run continuously:

1. Using START /B:
   START /B .\ifmrelay.exe -config relay_config.json

2. As Windows Service (requires NSSM or similar):
   nssm install IFMRelay "C:\IFMRelay\ifmrelay.exe" -config "C:\IFMRelay\relay_config.json"

TESTING
-------
1. Basic test:
   echo "test" | nc -u 127.0.0.1 50000

2. Throughput stress test:
   for /L %i in (1,1,10000) do @echo %i | nc -u 127.0.0.1 50000

3. Monitor with:
   - Task Manager (CPU/Memory)
   - Resource Monitor (Network activity)

LOG LEVELS
----------
- debug: Prints every received packet size and timestamp
- info: Standard operation logs + periodic statistics (default)
- error: Only errors and warnings

COMMAND LINE OPTIONS
--------------------
-config <path>    Path to configuration file (default: relay_config.json)

EXAMPLE CONFIGURATION
---------------------
{
  "listen_port": 50000,
  "targets": [
    {"host": "127.0.0.1", "port": 49983, "name": "VTubeStudio", "format": "raw"},
    {"host": "127.0.0.1", "port": 39539, "name": "Warudo", "format": "vsf"}
  ],
  "buffer_size": 4096,
  "log_level": "info",
  "stats_interval": 10
}

VSF FORMAT CONVERSION
---------------------
The relay automatically converts iFacialMocap JSON to VSF (Virtual Stream Format) 
when format: "vsf" is specified for a target. This enables full ARKit blendshape 
and head tracking support in Warudo, VSeeFace, and Virtual Motion Capture.

Conversion mapping:
- blendShapes → blendshapes (copied directly)
- rotation.x → head.pitch
- rotation.y → head.yaw  
- rotation.z → head.roll
- head.x/y/z → head.x/y/z (copied directly)
- Adds type: "vsf.blendshape" and current timestamp

Targets without "format" specified default to "raw" forwarding.

SUPPORT
-------
For issues, check:
1. Windows Event Viewer for system errors
2. Relay log output for specific error messages
3. Verify network connectivity between iOS device and PC
4. Ensure target applications are running and listening

Version: 1.1 (with VSF conversion support)
Build: Go 1.21+
