"""
Document RAG MCP Server — Real vector embeddings with ChromaDB.

Uses ChromaDB's built-in embedding function (default: all-MiniLM-L6-v2)
which handles model loading and embedding generation automatically.

This is REAL RAG — semantic search, not keyword matching.

Runs on port 3008.
"""

from flask import Flask, request, jsonify
import chromadb
import uuid
import os
import io
import re
import pdfplumber

app = Flask(__name__)

# ============ Initialize ChromaDB ============
# ChromaDB uses all-MiniLM-L6-v2 as its default embedding model
# The model is already downloaded and cached locally
print("Initializing ChromaDB (using all-MiniLM-L6-v2 embeddings)...", flush=True)

chroma_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "chroma_db")
chroma_client = chromadb.PersistentClient(path=chroma_path)
collection = chroma_client.get_or_create_collection(
    name="documents",
    metadata={"hnsw:space": "cosine"}
)
print(f"ChromaDB ready! Documents: {collection.count()} chunks", flush=True)

# ============ MCP Tool Definitions ============
TOOLS = [
    {
        "name": "upload_document",
        "description": "Upload a document to the RAG knowledge base. It will be chunked and embedded for semantic search.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "name": {"type": "string", "description": "Document name (e.g., 'resume', 'company-policy')"},
                "content": {"type": "string", "description": "Full text content of the document"}
            },
            "required": ["name", "content"]
        }
    },
    {
        "name": "ask_document",
        "description": "Ask a question about uploaded documents using semantic vector search. Finds relevant passages even if exact words don't match. Optionally filter to a specific document by name.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "Question to ask (e.g., 'What is the leave policy?')"},
                "document_name": {"type": "string", "description": "Optional: search only within this document (e.g., '176_ngo_reg_cert'). If not provided, searches all documents."},
                "num_results": {"type": "number", "description": "Number of relevant passages to return (default: 3)"}
            },
            "required": ["question"]
        }
    },
    {
        "name": "list_documents",
        "description": "List all documents in the RAG knowledge base",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    }
]

# ============ Helper Functions ============

def normalize_document_name(name):
    """Return a stable document key independent of path, extension, or casing."""
    raw_name = os.path.basename((name or "").strip())
    stem = os.path.splitext(raw_name)[0]
    normalized = re.sub(r"[^a-z0-9]+", "_", stem.lower()).strip("_")
    return normalized


def stored_document_names():
    """Return the unique document names currently persisted in ChromaDB."""
    if collection.count() == 0:
        return []

    metadata = collection.get(include=["metadatas"])["metadatas"]
    return sorted({item["doc_name"] for item in metadata if item.get("doc_name")})


def resolve_document_name(requested_name):
    """Resolve filename variants such as report.pdf to the stored document key."""
    if not requested_name:
        return None

    requested_key = normalize_document_name(requested_name)
    for stored_name in stored_document_names():
        if normalize_document_name(stored_name) == requested_key:
            return stored_name
    return None


def chunk_text(text, chunk_size=300):
    """Split text into chunks for better retrieval."""
    lines = text.replace("\n\n", "\n").split("\n")
    chunks = []
    current = ""

    for line in lines:
        line = line.strip()
        if not line:
            continue
        if len(current) + len(line) > chunk_size and current:
            chunks.append(current.strip())
            current = line
        else:
            current += "\n" + line if current else line

    if current.strip():
        chunks.append(current.strip())
    return chunks


def upload_doc(name, content):
    """Chunk document, embed with HuggingFace, store in ChromaDB."""
    name = normalize_document_name(name)
    if not name:
        return "Error: Document name is empty."

    chunks = chunk_text(content)
    if not chunks:
        return "Error: Document is empty."

    # Replace semantics: drop any existing chunks for this doc_name first,
    # so re-uploading a document refreshes it instead of duplicating chunks.
    existing = collection.get(where={"doc_name": name})
    if existing["ids"]:
        collection.delete(ids=existing["ids"])

    doc_id = str(uuid.uuid4())[:8]
    ids = [f"{doc_id}_chunk_{i}" for i in range(len(chunks))]

    # ChromaDB handles embedding automatically using our HuggingFace function
    collection.add(
        ids=ids,
        documents=chunks,
        metadatas=[{"doc_name": name, "chunk_index": i, "doc_id": doc_id} for i in range(len(chunks))]
    )

    return (
        f"Document '{name}' uploaded successfully!\n"
        f"  Chunks: {len(chunks)}\n"
        f"  Embedding model: all-MiniLM-L6-v2 (384 dimensions)\n"
        f"  Vector store: ChromaDB (cosine similarity)\n"
        f"  Total chunks in knowledge base: {collection.count()}"
    )


def ask_docs(question, num_results=3, document_name=None):
    """Semantic search through documents using vector similarity."""
    if collection.count() == 0:
        return "No documents uploaded yet. Use upload_document first."

    # Resolve paths/extensions/casing before applying ChromaDB's exact filter.
    resolved_name = resolve_document_name(document_name)
    if document_name and not resolved_name:
        available = ", ".join(stored_document_names()) or "none"
        return (
            f"DOCUMENT_NOT_FOUND: No uploaded document matches '{document_name}'. "
            f"Available documents: {available}. Do not infer an answer from chat "
            "history or another document."
        )

    where_filter = {"doc_name": resolved_name} if resolved_name else None
    matching_count = collection.count() if not resolved_name else len(
        collection.get(where={"doc_name": resolved_name}, include=[])["ids"]
    )

    # ChromaDB handles embedding the question + similarity search
    results = collection.query(
        query_texts=[question],
        n_results=min(num_results, matching_count),
        where=where_filter,
        include=["documents", "metadatas", "distances"]
    )

    if not results["documents"][0]:
        scope = f" in document '{resolved_name}'" if resolved_name else ""
        return (
            f"NO_RELEVANT_PASSAGES: No relevant information was found{scope} "
            f"for question '{question}'. Do not guess or use another document."
        )

    output = f"Found {len(results['documents'][0])} relevant passages (semantic search):\n\n"
    for i, (doc, meta, dist) in enumerate(zip(
        results["documents"][0], results["metadatas"][0], results["distances"][0]
    )):
        similarity = round((1 - dist) * 100, 1)
        output += f"--- From '{meta['doc_name']}' (similarity: {similarity}%) ---\n{doc}\n\n"

    return output


def list_docs():
    """List all documents."""
    if collection.count() == 0:
        return "No documents uploaded yet."

    all_meta = collection.get(include=["metadatas"])
    doc_names = {}
    for m in all_meta["metadatas"]:
        name = m["doc_name"]
        doc_names[name] = doc_names.get(name, 0) + 1

    output = f"Knowledge Base ({len(doc_names)} docs, {collection.count()} chunks):\n"
    for i, (name, count) in enumerate(doc_names.items(), 1):
        output += f"  {i}. {name} ({count} chunks)\n"
    output += "\nModel: all-MiniLM-L6-v2 | Store: ChromaDB"
    return output


# ============ MCP Endpoints ============

@app.route("/mcp/message", methods=["POST"])
def handle_mcp():
    data = request.json
    method = data.get("method", "")
    msg_id = data.get("id")
    params = data.get("params", {})

    if method == "initialize":
        return jsonify({"jsonrpc": "2.0", "id": msg_id, "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "docs-rag-server", "version": "2.0.0"}
        }})

    elif method == "tools/list":
        return jsonify({"jsonrpc": "2.0", "id": msg_id, "result": {"tools": TOOLS}})

    elif method == "tools/call":
        tool_name = params.get("name", "")
        args = params.get("arguments", {})

        if tool_name == "upload_document":
            text = upload_doc(args.get("name", ""), args.get("content", ""))
        elif tool_name == "ask_document":
            text = ask_docs(args.get("question", ""), int(args.get("num_results", 3)), args.get("document_name"))
        elif tool_name == "list_documents":
            text = list_docs()
        else:
            text = f"Unknown tool: {tool_name}"

        return jsonify({"jsonrpc": "2.0", "id": msg_id, "result": {
            "content": [{"type": "text", "text": text}], "isError": False
        }})

    return jsonify({"jsonrpc": "2.0", "id": msg_id, "error": {"code": -32601, "message": "Not found"}})


@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "documents": collection.count(), "model": "all-MiniLM-L6-v2", "store": "ChromaDB"})


@app.route("/upload", methods=["POST"])
def handle_file_upload():
    """Direct file upload endpoint — handles PDFs, text files, etc."""
    if "file" not in request.files:
        return jsonify({"error": "No file provided"}), 400

    file = request.files["file"]
    if file.filename == "":
        return jsonify({"error": "No file selected"}), 400

    doc_name = normalize_document_name(request.form.get("name", file.filename))

    # Extract text based on file type
    filename = file.filename.lower()
    try:
        if filename.endswith(".pdf"):
            # Parse PDF using pdfplumber (better text extraction than PyPDF2)
            with pdfplumber.open(io.BytesIO(file.read())) as pdf:
                text_content = ""
                for page in pdf.pages:
                    page_text = page.extract_text()
                    if page_text:
                        text_content += page_text + "\n"
            if not text_content.strip():
                return jsonify({"error": "Could not extract text from PDF. It might be a scanned image."}), 400
        else:
            # Plain text files
            text_content = file.read().decode("utf-8", errors="ignore")

        if not text_content.strip():
            return jsonify({"error": "File is empty or unreadable"}), 400

        # Upload to ChromaDB
        result = upload_doc(doc_name, text_content)
        return jsonify({"success": True, "message": result, "doc_name": doc_name, "chars": len(text_content)})

    except Exception as e:
        return jsonify({"error": f"Failed to process file: {str(e)}"}), 500


# ============ Pre-load Sample Document ============
SAMPLE = """MCP Gateway Project - Technical Documentation

Architecture Overview:
The MCP Gateway is a reverse proxy that aggregates multiple AI tool servers into one endpoint.
It uses the Model Context Protocol (MCP) which is JSON-RPC over HTTP.
The gateway handles tool discovery, health monitoring, request routing, and logging.

Team and Contributions:
Built by Varun Banda as a portfolio project demonstrating modern AI infrastructure.
The project showcases understanding of distributed systems, protocols, and AI tool integration.

Performance:
Average latency through the gateway is 2-5ms overhead.
Health checks run every 10 seconds. The system handles concurrent requests safely using mutexes.

Security Considerations:
Currently runs locally. For production, add API key authentication, rate limiting, and HTTPS.
The GROQ API key should be stored as an environment variable, never in code.

How to Add a New Server:
1. Create a new MCP server that responds to initialize, tools/list, and tools/call
2. Add it to config.yaml with name, URL, and enabled: true
3. Restart the gateway - it auto-discovers the new tools

Deployment Options:
- Local development: ./start.sh
- Docker: docker-compose up (planned)
- Cloud: Fly.io or Railway with free tier
"""

if collection.count() == 0:
    print("Loading sample document into ChromaDB...")
    upload_doc("project-docs", SAMPLE)
    print("Done!")


if __name__ == "__main__":
    print(f"\nRAG Server ready on http://localhost:3008", flush=True)
    print(f"  Model: all-MiniLM-L6-v2 | Store: ChromaDB | Docs: {collection.count()} chunks\n", flush=True)
    app.run(host="0.0.0.0", port=3008)
