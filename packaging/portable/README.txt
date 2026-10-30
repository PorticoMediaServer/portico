Portico Media Server portable package

Unpack the entire archive into a directory where your account can write.
On Linux run ./start-portico.sh. On Windows run start-portico.cmd.
Open http://127.0.0.1:32500/ in your browser.

The launcher finds the web interface and FFmpeg beside the server. Data uses
~/.local/share/portico on Linux, ~/Library/Application Support/Portico on macOS,
and %LOCALAPPDATA%\Portico on Windows. Transcodes use a separate platform cache
folder. Stop the server before moving its data folder.

To store state elsewhere, set PORTICO_DATA_DIR to an absolute directory
path before running the launcher. PORTICO_WEB_DIR can also be overridden.
Use the same state directory on subsequent starts. Run under an account
with access to your selected media folders; portable launchers do not
install a system service or change firewall rules.
