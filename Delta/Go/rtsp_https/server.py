


import http.server
from socketserver import TCPServer

PORT = 8444

class ReuseTCPServer(TCPServer):
    allow_reuse_address = True

Handler = http.server.SimpleHTTPRequestHandler

httpd = ReuseTCPServer(("0.0.0.0", PORT), Handler)

print(f"Serving HTTP at http://0.0.0.0:{PORT}")
httpd.serve_forever()









# import http.server
# import ssl
# from socketserver import TCPServer

# PORT = 8444

# class ReuseTCPServer(TCPServer):
#     allow_reuse_address = True  # 🔥 Important fix

# Handler = http.server.SimpleHTTPRequestHandler

# httpd = ReuseTCPServer(("0.0.0.0", PORT), Handler)

# context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
# context.load_cert_chain(certfile="cert.pem", keyfile="key.pem")

# httpd.socket = context.wrap_socket(httpd.socket, server_side=True)

# print(f"Serving HTTPS at https://0.0.0.0:{PORT}")
# httpd.serve_forever()





# import http.server
# import ssl
# from socketserver import TCPServer

# PORT = 8443

# Handler = http.server.SimpleHTTPRequestHandler

# httpd = TCPServer(("0.0.0.0", PORT), Handler)

# # Create SSL context (modern way)
# context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
# context.load_cert_chain(certfile="cert.pem", keyfile="key.pem")

# # Wrap socket
# httpd.socket = context.wrap_socket(httpd.socket, server_side=True)

# print(f"Serving HTTPS at https://0.0.0.0:{PORT}")
# httpd.serve_forever()

