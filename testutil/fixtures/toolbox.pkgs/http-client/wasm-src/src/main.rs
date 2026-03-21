use std::io::{Read, Write};
use std::net::TcpStream;

fn main() {
    match do_request() {
        Ok(()) => {}
        Err(e) => {
            eprintln!("http error: {e}");
            std::process::exit(1);
        }
    }
}

fn do_request() -> Result<(), Box<dyn std::error::Error>> {
    let mut stream = TcpStream::connect("httpbin.org:80")?;

    let request = "GET /get HTTP/1.1\r\nHost: httpbin.org\r\nConnection: close\r\n\r\n";
    stream.write_all(request.as_bytes())?;

    let mut response = String::new();
    stream.read_to_string(&mut response)?;

    // Parse status line
    let status_line = response.lines().next().unwrap_or("");
    let status = if status_line.contains("200") {
        "200 OK"
    } else {
        status_line
    };

    // Find body (after empty line)
    let body = if let Some(pos) = response.find("\r\n\r\n") {
        &response[pos + 4..]
    } else {
        ""
    };

    println!("Status: {status}\n{body}");

    Ok(())
}
