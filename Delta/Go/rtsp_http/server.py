import http.server
import socketserver
import os

PORT = 8445
BASE_DIR = os.path.dirname(os.path.abspath(__file__))

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=BASE_DIR, **kwargs)

    def end_headers(self):
        try:
            self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
            self.send_header("Pragma", "no-cache")
            self.send_header("Expires", "0")
            self.send_header("Access-Control-Allow-Origin", "*")
            super().end_headers()
        except BrokenPipeError:
            pass

    def log_message(self, format, *args):
        pass   # disables spam logs (important for performance)

class ReuseServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

if __name__ == "__main__":
    os.chdir(BASE_DIR)
    print(f"🚀 Low-latency HLS running: http://0.0.0.0:{PORT}")
    httpd = ReuseServer(("0.0.0.0", PORT), Handler)
    httpd.serve_forever()



# import http.server
# import socketserver
# import os

# PORT = 8445
# BASE_DIR = os.path.dirname(os.path.abspath(__file__))

# class Handler(http.server.SimpleHTTPRequestHandler):
#     def __init__(self, *args, **kwargs):
#         super().__init__(*args, directory=BASE_DIR, **kwargs)

#     def end_headers(self):
#         self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
#         self.send_header("Access-Control-Allow-Origin", "*")
#         super().end_headers()

#     def log_message(self, format, *args):
#         print("[%s] %s" % (self.address_string(), format % args))


# class ReuseServer(socketserver.TCPServer):
#     allow_reuse_address = True


# if __name__ == "__main__":
#     os.chdir(BASE_DIR)

#     print(f"🚀 Server running: http://0.0.0.0:{PORT}")
#     print(f"📺 HLS URL: http://192.168.96.72:{PORT}/hls/playlist.m3u8")

#     httpd = ReuseServer(("0.0.0.0", PORT), Handler)
#     httpd.serve_forever()

# import http.server
# import socketserver
# import os

# PORT = 8445
# DIRECTORY = os.path.join(os.path.dirname(__file__), "hls")

# class HLSHandler(http.server.SimpleHTTPRequestHandler):
#     def __init__(self, *args, **kwargs):
#         super().__init__(*args, directory=DIRECTORY, **kwargs)

#     def end_headers(self):
#         self.send_header("Cache-Control", "no-cache, no-store, must-revalidate")
#         self.send_header("Pragma", "no-cache")
#         self.send_header("Expires", "0")
#         self.send_header("Access-Control-Allow-Origin", "*")
#         super().end_headers()

# class ReuseTCPServer(socketserver.TCPServer):
#     allow_reuse_address = True

# if __name__ == "__main__":
#     httpd = ReuseTCPServer(("0.0.0.0", PORT), HLSHandler)
#     print(f"🚀 HLS Server running at http://0.0.0.0:{PORT}")
#     print(f"📺 Stream: http://localhost:{PORT}/playlist.m3u8")
#     httpd.serve_forever()