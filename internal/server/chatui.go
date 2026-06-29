package server

const chatPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MCP Gateway - AI Chat</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #0f1117; color: #e4e4e7; height: 100vh;
            display: flex; overflow: hidden;
        }

        /* Hamburger mobile toggle */
        .hamburger-btn {
            display: none; width: 36px; height: 36px; border: none;
            background: transparent; color: #a1a1aa; cursor: pointer;
            align-items: center; justify-content: center; border-radius: 8px;
            transition: all 0.2s;
        }
        .hamburger-btn:hover { background: #2a2b35; color: #e4e4e7; }
        .hamburger-btn svg { width: 20px; height: 20px; }

        /* Scroll to bottom button */
        .scroll-bottom-btn {
            position: fixed; bottom: 90px; right: 24px; width: 36px; height: 36px;
            border-radius: 50%; border: 1px solid #2a2b35; background: #1a1b23;
            color: #a1a1aa; cursor: pointer; display: none; align-items: center;
            justify-content: center; font-size: 18px; z-index: 50;
            transition: all 0.2s; box-shadow: 0 2px 8px rgba(0,0,0,0.4);
        }
        .scroll-bottom-btn:hover { background: #a855f720; color: #a855f7; border-color: #a855f7; }
        .scroll-bottom-btn.visible { display: flex; }

        /* Message timestamp */
        .message .timestamp {
            font-size: 10px; color: #52525b; margin-top: 4px; opacity: 0;
            transition: opacity 0.2s;
        }
        .message:hover .timestamp { opacity: 1; }

        /* Sidebar */
        .sidebar {
            width: 260px; background: #1a1b23; border-right: 1px solid #2a2b35;
            display: flex; flex-direction: column; height: 100vh; flex-shrink: 0;
        }
        .sidebar-header {
            padding: 16px; border-bottom: 1px solid #2a2b35;
            display: flex; align-items: center; justify-content: space-between;
        }
        .sidebar-header h2 { font-size: 14px; color: #a1a1aa; }
        .new-chat-btn {
            padding: 6px 12px; border-radius: 6px; border: 1px solid #2a2b35;
            background: transparent; color: #a855f7; font-size: 12px;
            cursor: pointer; transition: all 0.2s;
        }
        .new-chat-btn:hover { background: #a855f720; border-color: #a855f7; }

        .sessions-list {
            flex: 1; overflow-y: auto; padding: 8px;
        }
        .session-item {
            padding: 10px 12px; border-radius: 8px; margin-bottom: 4px;
            cursor: pointer; font-size: 13px; color: #a1a1aa;
            transition: all 0.2s; display: flex; justify-content: space-between;
            align-items: center;
        }
        .session-item:hover { background: #2a2b35; color: #e4e4e7; }
        .session-item.active { background: #a855f720; color: #a855f7; border: 1px solid #a855f740; }
        .session-item .title { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .session-item .delete-btn {
            opacity: 0; color: #ef4444; cursor: pointer; font-size: 16px; padding: 0 4px;
        }
        .session-item:hover .delete-btn { opacity: 1; }
        .session-item .time { font-size: 10px; color: #52525b; margin-top: 2px; }

        .sidebar-footer {
            padding: 12px 16px; border-top: 1px solid #2a2b35;
            font-size: 11px; color: #52525b; text-align: center;
        }
        .sidebar-footer a { color: #a855f7; text-decoration: none; }

        /* Main Chat Area */
        .main { flex: 1; display: flex; flex-direction: column; height: 100vh; }

        .header {
            background: #1a1b23; border-bottom: 1px solid #2a2b35;
            padding: 12px 24px; display: flex; align-items: center; justify-content: space-between;
        }
        .header-left { display: flex; align-items: center; gap: 10px; }
        .header h1 { font-size: 16px; color: #fff; }
        .header .badge { font-size: 10px; padding: 2px 8px; border-radius: 6px; background: #22c55e20; color: #22c55e; }
        .header a { color: #71717a; text-decoration: none; font-size: 12px; }
        .header a:hover { color: #a855f7; }

        .chat-container { flex: 1; overflow-y: auto; padding: 20px 24px; scroll-behavior: smooth; }

        .welcome { text-align: center; padding: 60px 20px; }
        .welcome-card { background: linear-gradient(135deg, #1a1b2380 0%, #a855f710 100%); border: 1px solid #a855f730; border-radius: 16px; padding: 40px 32px; display: inline-block; }
        .welcome h2 { font-size: 22px; color: #fff; margin-bottom: 8px; }
        .welcome p { color: #71717a; font-size: 13px; margin-bottom: 20px; }
        .capabilities { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 8px; max-width: 550px; margin: 0 auto; }
        .capability { padding: 10px 12px; border-radius: 8px; background: #1a1b23; border: 1px solid #2a2b35; font-size: 12px; color: #a1a1aa; cursor: pointer; transition: all 0.2s; text-align: left; }
        .capability:hover { border-color: #a855f7; color: #e4e4e7; background: #a855f710; transform: translateY(-1px); }

        .message { margin-bottom: 14px; display: flex; gap: 10px; animation: fadeIn 0.3s ease; }
        .message.user { justify-content: flex-end; }
        @keyframes fadeIn { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }

        .message .bubble { max-width: 72%; padding: 11px 15px; border-radius: 16px; font-size: 14px; line-height: 1.6; white-space: pre-wrap; }
        .message.user .bubble { background: linear-gradient(135deg, #a855f7, #7c3aed); color: #fff; border-bottom-right-radius: 4px; }
        .message.ai .bubble { background: #1a1b23; border: 1px solid #2a2b35; color: #e4e4e7; border-bottom-left-radius: 4px; }

        .message .meta { display: flex; gap: 6px; align-items: center; margin-top: 6px; flex-wrap: wrap; }
        .message .tool-badge { font-size: 10px; padding: 2px 6px; border-radius: 4px; background: #a855f720; color: #a855f7; }
        .message .latency-badge { font-size: 10px; color: #52525b; }
        .message .steps { margin-top: 8px; padding: 8px 10px; border-radius: 6px; background: #0f111780; border: 1px solid #2a2b35; font-size: 11px; }
        .message .step-item { display: flex; align-items: center; gap: 6px; padding: 3px 0; color: #71717a; }
        .message .step-dot { width: 5px; height: 5px; border-radius: 50%; background: #a855f7; }

        .typing { display: none; margin-bottom: 14px; }
        .typing .dots { display: flex; gap: 4px; padding: 12px 16px; background: #1a1b23; border: 1px solid #2a2b35; border-radius: 16px; width: fit-content; border-bottom-left-radius: 4px; }
        .typing .dots span { width: 7px; height: 7px; border-radius: 50%; background: #a855f7; animation: bounce 1.4s infinite both; }
        .typing .dots span:nth-child(2) { animation-delay: 0.16s; }
        .typing .dots span:nth-child(3) { animation-delay: 0.32s; }
        @keyframes bounce { 0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; } 40% { transform: scale(1); opacity: 1; } }

        .input-area { background: #1a1b23; border-top: 1px solid #2a2b35; padding: 14px 20px; }
        .input-wrapper { display: flex; gap: 8px; align-items: center; background: #0f1117; border: 1px solid #2a2b35; border-radius: 14px; padding: 5px 5px 5px 16px; transition: border-color 0.2s; }
        .input-wrapper:focus-within { border-color: #a855f7; }
        .input-wrapper input { flex: 1; padding: 9px 0; border: none; background: transparent; color: #e4e4e7; font-size: 14px; outline: none; }
        .input-wrapper input::placeholder { color: #52525b; }

        .icon-btn { width: 36px; height: 36px; border-radius: 10px; border: none; display: flex; align-items: center; justify-content: center; cursor: pointer; transition: all 0.2s; background: transparent; }
        .icon-btn svg { width: 18px; height: 18px; }
        .icon-btn:hover { background: #2a2b35; }
        .mic-btn { color: #71717a; }
        .mic-btn:hover { color: #e4e4e7; }
        .mic-btn.recording { color: #ef4444; background: #ef444420; animation: pulse-red 1.5s infinite; }
        @keyframes pulse-red { 0%, 100% { box-shadow: 0 0 0 0 #ef444440; } 50% { box-shadow: 0 0 0 6px transparent; } }
        .send-btn { background: #a855f7; color: #fff; border-radius: 10px; }
        .send-btn:hover { background: #9333ea; }
        .send-btn:disabled { opacity: 0.3; cursor: not-allowed; }

        .voice-status { text-align: center; font-size: 11px; color: #ef4444; margin-top: 6px; display: none; }

        /* Mobile sidebar backdrop */
        .sidebar-backdrop {
            display: none; position: fixed; inset: 0; background: rgba(0,0,0,0.55);
            z-index: 99; backdrop-filter: blur(2px);
        }
        .sidebar-backdrop.visible { display: block; }

        @media (max-width: 768px) {
            .sidebar {
                position: fixed; left: -280px; top: 0; z-index: 100;
                width: 280px; height: 100%; height: 100dvh;
                transition: left 0.28s cubic-bezier(.4,0,.2,1);
                box-shadow: none;
            }
            .sidebar.open { left: 0; box-shadow: 6px 0 24px rgba(0,0,0,0.6); }
            .hamburger-btn { display: flex; }
            .message .bubble { max-width: 88%; font-size: 13px; }
            body { overflow: hidden; height: 100%; height: 100dvh; }
            .main { height: 100dvh; min-height: 0; }
            .chat-container { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; }
            .input-area { position: sticky; bottom: 0; z-index: 10; flex-shrink: 0; }
            .input-wrapper input::placeholder { font-size: 13px; }
            .scroll-bottom-btn { right: 12px; bottom: 80px; }
            /* Larger tap targets */
            .session-item { padding: 13px 12px; }
            .new-chat-btn { padding: 8px 14px; font-size: 13px; }
        }
    </style>
</head>
<body>
    <!-- Mobile sidebar backdrop -->
    <div class="sidebar-backdrop" id="sidebar-backdrop" onclick="closeSidebar()"></div>

    <!-- Sidebar -->
    <div class="sidebar" id="sidebar">
        <div class="sidebar-header">
            <h2>Chat History</h2>
            <button class="new-chat-btn" onclick="newSession()">+ New</button>
        </div>
        <div class="sessions-list" id="sessions-list"></div>
        <div class="sidebar-footer">
            <a href="/">Dashboard</a> | 8 servers, 20 tools
        </div>
    </div>

    <!-- Main -->
    <div class="main">
        <div class="header">
            <div class="header-left">
                <button class="hamburger-btn" onclick="toggleSidebar()" title="Toggle sidebar">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" /></svg>
                </button>
                <h1>MCP Gateway AI</h1>
                <span class="badge">Agent + RAG</span>
            </div>
            <a href="/">Dashboard</a>
        </div>

        <div class="chat-container" id="chat-container">
            <div class="welcome" id="welcome">
                <div class="welcome-card">
                    <h2>Ask me anything</h2>
                    <p>Multi-step AI agent with real tools. I remember our conversation.</p>
                    <div class="capabilities">
                        <div class="capability" onclick="ask(this)">Weather in Tokyo</div>
                        <div class="capability" onclick="ask(this)">Bitcoin price now</div>
                        <div class="capability" onclick="ask(this)">Latest tech news</div>
                        <div class="capability" onclick="ask(this)">Who is Elon Musk?</div>
                        <div class="capability" onclick="ask(this)">Compare Delhi &amp; Mumbai weather</div>
                        <div class="capability" onclick="ask(this)">Save a note about my project</div>
                    </div>
                </div>
            </div>
        </div>
        <button class="scroll-bottom-btn" id="scroll-bottom-btn" onclick="scrollToBottom()" title="Scroll to bottom">↓</button>

        <div class="typing" id="typing">
            <div class="dots"><span></span><span></span><span></span></div>
        </div>

        <div class="input-area">
            <div class="input-wrapper">
                <input type="file" id="file-input" accept=".txt,.md,.csv,.json,.py,.js,.go,.html,.pdf" style="display:none;" onchange="handleFileUpload()" />
                <button class="icon-btn mic-btn" id="upload-btn" onclick="document.getElementById('file-input').click()" title="Upload document">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" /></svg>
                </button>
                <input type="text" id="user-input" placeholder="Ask anything... (I remember context)" autocomplete="off" />
                <button class="icon-btn mic-btn" id="mic-btn" onclick="toggleVoice()" title="Voice input">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M12 18.75a6 6 0 006-6v-1.5m-6 7.5a6 6 0 01-6-6v-1.5m6 7.5v3.75m-3.75 0h7.5M12 15.75a3 3 0 01-3-3V4.5a3 3 0 116 0v8.25a3 3 0 01-3 3z" /></svg>
                </button>
                <button class="icon-btn send-btn" id="send-btn" onclick="sendMessage()">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="2" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M6 12L3.269 3.126A59.768 59.768 0 0121.485 12 59.77 59.77 0 013.27 20.876L5.999 12zm0 0h7.5" /></svg>
                </button>
            </div>
            <div class="voice-status" id="voice-status">Listening...</div>
            <div class="voice-status" id="upload-status" style="color:#22c55e;"></div>
        </div>
    </div>

    <script>
        // ===== Auth =====
        function getToken() { return localStorage.getItem('mcp_token'); }
        function authHeaders() { return { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + getToken() }; }
        function clearChatStorage() {
            localStorage.removeItem('chat_messages');
            localStorage.removeItem('local_sessions');
            localStorage.removeItem('local_session_id');
        }
        function redirectToLogin() {
            clearChatStorage();
            window.location.href = '/';
        }

        // ===== State Management (localStorage-backed) =====
        function generateId() { return 'local_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8); }

        function getStoredMessages() {
            try { return JSON.parse(localStorage.getItem('chat_messages') || '{}'); } catch { return {}; }
        }
        function saveStoredMessages(store) {
            localStorage.setItem('chat_messages', JSON.stringify(store));
        }
        function getLocalSessions() {
            try { return JSON.parse(localStorage.getItem('local_sessions') || '[]'); } catch { return []; }
        }
        function saveLocalSessions(list) {
            localStorage.setItem('local_sessions', JSON.stringify(list));
        }
        function getLocalTitle(id) {
            const msgs = messageStore[id] || [];
            if (msgs.length === 0) return 'New Chat';
            const first = msgs.find(m => m.role === 'user');
            if (!first) return 'New Chat';
            const t = first.content;
            return t.length > 50 ? t.slice(0, 50) + '...' : t;
        }

        let sessions = [];
        let localSessions = getLocalSessions();
        let messageStore = getStoredMessages();
        let currentSessionId = localStorage.getItem('local_session_id') || generateId();
        localStorage.setItem('local_session_id', currentSessionId);

        function _buildAllSessions() {
            const serverIds = new Set(sessions.map(s => s.id));
            const local = localSessions
                .filter(s => !serverIds.has(s.id))
                .map(s => ({...s, _local: true, title: getLocalTitle(s.id)}));
            return [...sessions, ...local];
        }

        let _serverAvailable = false;
        const _welcome = document.getElementById('welcome');
        const _chatContainer = document.getElementById('chat-container');

        async function loadSessionsFromServer() {
            try {
                const resp = await fetch('/api/chat/sessions', { headers: authHeaders() });
                if (resp.status === 401) { redirectToLogin(); return; }
                if (resp.status === 404 || resp.status === 405) {
                    _serverAvailable = false;
                    _syncLocalSidebar();
                    return;
                }
                const data = await resp.json();
                sessions = data.sessions || [];
                _serverAvailable = true;
                _syncLocalSidebar();
            } catch { sessions = []; _serverAvailable = false; _syncLocalSidebar(); }
        }

        function _syncLocalSidebar() {
            renderSidebar(_buildAllSessions());
            const all = _buildAllSessions();
            if (all.length > 0) {
                if (!all.find(s => s.id === currentSessionId)) {
                    doSwitch(all[0].id);
                }
            } else if (_serverAvailable) {
                createNewSession();
            } else {
                _newLocalSession();
            }
        }

        function doSwitch(id) {
            if (!id) return;
            currentSessionId = id;
            localStorage.setItem('local_session_id', currentSessionId);
            renderSidebar();
            loadMessages(id);
            closeSidebar(); // auto-close on mobile after picking a session
        }

        document.getElementById('sessions-list').onclick = function(e) {
            const del = e.target.closest('.delete-btn');
            if (del) {
                e.stopPropagation();
                deleteSession(del.dataset.did, e);
                return;
            }
            const item = e.target.closest('.session-item');
            if (item) doSwitch(item.dataset.sid);
        };

        async function createNewSession() {
            try {
                const resp = await fetch('/api/chat/sessions', {
                    method: 'POST',
                    headers: authHeaders(),
                    body: JSON.stringify({ title: 'New Chat' })
                });
                if (resp.status === 401) { redirectToLogin(); return; }
                if (resp.status === 404 || resp.status === 405) {
                    _newLocalSession();
                    return;
                }
                const session = await resp.json();
                sessions.unshift(session);
                doSwitch(session.id);
            } catch {}
        }

        function _newLocalSession() {
            currentSessionId = generateId();
            localStorage.setItem('local_session_id', currentSessionId);
            localSessions.unshift({id: currentSessionId, title: 'New Chat', created_at: new Date().toISOString()});
            saveLocalSessions(localSessions);
            if (!messageStore[currentSessionId]) messageStore[currentSessionId] = [];
            saveStoredMessages(messageStore);
            _syncLocalSidebar();
            _renderLocalMessages();
            closeSidebar();
        }

        function newSession() {
            if (_serverAvailable) { createNewSession(); }
            else { _newLocalSession(); }
        }

        async function deleteSession(id, e) {
            e.stopPropagation();
            // Try server
            try {
                await fetch('/api/chat/sessions/' + id, { method: 'DELETE', headers: authHeaders() });
            } catch {}
            // Remove locally
            sessions = sessions.filter(s => s.id !== id);
            localSessions = localSessions.filter(s => s.id !== id);
            saveLocalSessions(localSessions);
            delete messageStore[id];
            saveStoredMessages(messageStore);
            _syncLocalSidebar();
            const all = _buildAllSessions();
            if (currentSessionId === id) {
                if (all.length > 0) doSwitch(all[0].id);
                else _newLocalSession();
            }
        }

        async function loadMessages(id) {
            // Try server first
            try {
                const resp = await fetch('/api/chat/sessions/' + id + '/messages', { headers: authHeaders() });
                if (resp.status === 401) { redirectToLogin(); return; }
                if (resp.ok) {
                    const data = await resp.json();
                    const msgs = data.messages || [];
                    if (msgs.length > 0) {
                        messageStore[id] = msgs.map(m => ({role: m.role, content: m.content, meta: m.meta}));
                        saveStoredMessages(messageStore);
                    }
                }
            } catch {}

            // Fallback to local storage
            const localMsgs = messageStore[id] || [];
            _chatContainer.textContent = '';
            if (localMsgs.length > 0) {
                if (_welcome) _welcome.style.display = 'none';
                localMsgs.forEach(m => addMessageToDOM(m.content, m.role, m.meta));
                scrollToBottom();
                return;
            }

            if (_welcome) {
                _welcome.style.display = 'block';
                _chatContainer.appendChild(_welcome);
            }
        }

        // ===== Rendering =====
        function renderSidebar(allSessions) {
            const list = document.getElementById('sessions-list');
            if (!list) return;
            const items = allSessions || _buildAllSessions();
            list.textContent = '';
            for (const s of items) {
                const div = document.createElement('div');
                div.className = 'session-item' + (s.id === currentSessionId ? ' active' : '');
                div.dataset.sid = s.id;
                const inner = document.createElement('div');
                const titleEl = document.createElement('div');
                titleEl.className = 'title';
                titleEl.textContent = s.title || 'Chat';
                const timeEl = document.createElement('div');
                timeEl.className = 'time';
                timeEl.textContent = s.created_at ? new Date(s.created_at).toLocaleDateString() : '';
                inner.appendChild(titleEl);
                inner.appendChild(timeEl);
                const del = document.createElement('span');
                del.className = 'delete-btn';
                del.dataset.did = s.id;
                del.textContent = '\u00D7';
                div.appendChild(inner);
                div.appendChild(del);
                list.appendChild(div);
            }
        }

        function _renderLocalMessages() {
            const msgs = messageStore[currentSessionId] || [];
            _chatContainer.textContent = '';
            if (msgs.length > 0) {
                if (_welcome) _welcome.style.display = 'none';
                msgs.forEach(m => addMessageToDOM(m.content, m.role, m.meta));
                scrollToBottom();
            } else {
                if (_welcome) {
                    _welcome.style.display = 'block';
                    _chatContainer.appendChild(_welcome);
                }
            }
        }

        function _escHtml(s) { const d = document.createElement('div'); d.textContent = String(s || ''); return d.innerHTML; }

        function addMessageToDOM(text, role, meta) {
            const div = document.createElement('div');
            div.className = 'message ' + role;

            const now = new Date().toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
            let metaHTML = '<div class="timestamp">' + now + '</div>';

            if (role === 'ai' && meta) {
                if (meta.steps && meta.steps.length > 0) {
                    metaHTML += '<div class="steps">';
                    meta.steps.forEach(s => {
                        const args = s.arguments ? Object.values(s.arguments).map(_escHtml).join(', ') : '';
                        metaHTML += '<div class="step-item"><span class="step-dot"></span>' + _escHtml(s.tool_name) + '(' + args + ')</div>';
                    });
                    metaHTML += '</div>';
                }
                metaHTML += '<div class="meta">';
                if (meta.tools && meta.tools.length > 0) meta.tools.forEach(t => { metaHTML += '<span class="tool-badge">' + _escHtml(t) + '</span>'; });
                if (meta.latency) metaHTML += '<span class="latency-badge">' + _escHtml(String(meta.latency)) + 'ms</span>';
                metaHTML += '</div>';
            }

            div.innerHTML = '<div class="bubble">' + formatText(text || '') + metaHTML + '</div>';
            _chatContainer.appendChild(div);
        }

        // ===== Sending Messages =====
        let pendingApprovalId = null;
        let pendingApprovalMessage = null;

        async function sendMessage() {
            const input = document.getElementById('user-input');
            const msg = input.value.trim();
            if (!msg || !currentSessionId) return;

            await doSend(msg, null);
            input.focus();
            scrollToBottom();
        }

        async function doSend(msg, approvalId) {
            if (_welcome) _welcome.style.display = 'none';

            if (!approvalId) {
                addMessageToDOM(msg, 'user');
                document.getElementById('user-input').value = '';
                if (!messageStore[currentSessionId]) messageStore[currentSessionId] = [];
                messageStore[currentSessionId].push({role: 'user', content: msg});
                saveStoredMessages(messageStore);
            }

            document.getElementById('send-btn').disabled = true;
            document.getElementById('typing').style.display = 'block';
            scrollToBottom();

            try {
                const body = { message: msg, session_id: currentSessionId };
                if (approvalId) body.approval_id = approvalId;

                const resp = await fetch('/api/chat', {
                    method: 'POST',
                    headers: authHeaders(),
                    body: JSON.stringify(body)
                });
                if (resp.status === 401) { redirectToLogin(); return; }
                const data = await resp.json();

                document.getElementById('typing').style.display = 'none';
                document.getElementById('send-btn').disabled = false;

                // Handle pending approval (human-in-the-loop)
                if (data.status === 'pending_approval') {
                    pendingApprovalId = data.approval_id;
                    pendingApprovalMessage = msg;
                    showApprovalPrompt(data);
                    return;
                }

                const meta = data.error ? {} : { tools: data.tools_used, steps: data.steps, latency: data.latency, num_tasks: data.num_tasks };
                const answer = data.error ? 'Error: ' + data.error : data.answer;
                addMessageToDOM(answer, 'ai', meta);
                if (!messageStore[currentSessionId]) messageStore[currentSessionId] = [];
                messageStore[currentSessionId].push({role: 'ai', content: answer, meta: meta});
                saveStoredMessages(messageStore);

                // Reload sidebar to get updated title
                try {
                    const sresp = await fetch('/api/chat/sessions', { headers: authHeaders() });
                    if (sresp.ok) {
                        const sdata = await sresp.json();
                        sessions = sdata.sessions || [];
                        renderSidebar();
                    } else {
                        // Update local session title
                        const idx = localSessions.findIndex(s => s.id === currentSessionId);
                        if (idx >= 0) {
                            localSessions[idx].title = getLocalTitle(currentSessionId);
                            saveLocalSessions(localSessions);
                            renderSidebar();
                        }
                    }
                } catch {}
            } catch (e) {
                document.getElementById('typing').style.display = 'none';
                document.getElementById('send-btn').disabled = false;
                addMessageToDOM('Connection error. Is the gateway running?', 'ai', {});
            }

            scrollToBottom();
        }

        function showApprovalPrompt(data) {
            const div = document.createElement('div');
            div.className = 'message ai';
            div.id = 'approval-prompt';

            let planInfo = '';
            if (data.plan_tasks && data.plan_tasks.length > 0) {
                planInfo = '<div class="steps"><strong>Planned tasks:</strong>';
                data.plan_tasks.forEach(t => {
                    const args = t.arguments ? Object.entries(t.arguments).map(([k,v]) => k+'='+v).join(', ') : '';
                    planInfo += '<div class="step-item"><span class="step-dot"></span>' + t.tool + '(' + args + ') — ' + (t.description || '') + '</div>';
                });
                planInfo += '</div>';
            }

            div.innerHTML = '<div class="bubble" style="border-color:#f9731640;">' +
                '<strong style="color:#f97316;">Action Required</strong><br><br>' +
                'This action needs your approval before proceeding:' +
                planInfo +
                '<div style="margin-top:12px;display:flex;gap:8px;">' +
                '<button class="send-btn" onclick="approveAction()" style="background:#22c55e;">Approve</button>' +
                '<button class="send-btn" onclick="rejectAction()" style="background:#ef4444;">Reject</button>' +
                '</div></div>';
            _chatContainer.appendChild(div);
            scrollToBottom();
        }

        async function approveAction() {
            if (!pendingApprovalId) return;
            document.getElementById('send-btn').disabled = true;
            document.getElementById('typing').style.display = 'block';
            scrollToBottom();

            try {
                await fetch('/api/approvals/' + pendingApprovalId + '/approve', {
                    method: 'POST', headers: authHeaders()
                });
                document.getElementById('approval-prompt').remove();
                await doSend(pendingApprovalMessage, pendingApprovalId);
                pendingApprovalId = null;
                pendingApprovalMessage = null;
            } catch (e) {
                document.getElementById('typing').style.display = 'none';
                document.getElementById('send-btn').disabled = false;
                addMessageToDOM('Error approving action: ' + e.message, 'ai', {});
            }
        }

        async function rejectAction() {
            if (!pendingApprovalId) return;
            try {
                await fetch('/api/approvals/' + pendingApprovalId + '/reject', {
                    method: 'POST', headers: authHeaders()
                });
                document.getElementById('approval-prompt').remove();
                addMessageToDOM('Action rejected. How else can I help you?', 'ai', {});
                pendingApprovalId = null;
                pendingApprovalMessage = null;
            } catch (e) {
                addMessageToDOM('Error rejecting action: ' + e.message, 'ai', {});
            }
        }

        // ===== Voice =====
        let isRecording = false, recognition = null;
        if ('webkitSpeechRecognition' in window || 'SpeechRecognition' in window) {
            const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
            recognition = new SR();
            recognition.continuous = false; recognition.interimResults = true; recognition.lang = 'en-US';
            recognition.onresult = e => {
                let t = ''; for (let i = e.resultIndex; i < e.results.length; i++) t += e.results[i][0].transcript;
                document.getElementById('user-input').value = t;
                if (e.results[e.results.length-1].isFinal) { stopVoice(); setTimeout(sendMessage, 300); }
            };
            recognition.onend = stopVoice;
        } else { document.getElementById('mic-btn').style.display = 'none'; }

        function toggleVoice() { isRecording ? stopVoice() : startVoice(); }
        function startVoice() { if(!recognition)return; isRecording=true; document.getElementById('mic-btn').classList.add('recording'); document.getElementById('voice-status').style.display='block'; recognition.start(); }
        function stopVoice() { isRecording=false; document.getElementById('mic-btn').classList.remove('recording'); document.getElementById('voice-status').style.display='none'; try{recognition.stop();}catch(e){} }

        // ===== File Upload =====
        async function handleFileUpload() {
            const fileInput = document.getElementById('file-input');
            const file = fileInput.files[0];
            if (!file) return;

            const statusEl = document.getElementById('upload-status');
            statusEl.style.display = 'block';
            statusEl.textContent = 'Uploading ' + file.name + '...';

            try {
                const resp = await fetch('/api/upload', {
                    method: 'POST',
                    headers: {'Authorization': 'Bearer ' + getToken()},
                    body: (() => { const fd = new FormData(); fd.append('file', file); fd.append('name', file.name.replace(/\.[^.]+$/, '')); return fd; })()
                });
                if (resp.status === 401) { redirectToLogin(); return; }
                const data = await resp.json();
                if (data.error) {
                    statusEl.style.color = '#ef4444'; statusEl.textContent = 'Error: ' + data.error;
                    setTimeout(() => { statusEl.style.display = 'none'; statusEl.style.color = '#22c55e'; }, 4000);
                } else {
                    statusEl.textContent = 'Uploaded: ' + file.name;
                    setTimeout(() => { statusEl.style.display = 'none'; }, 3000);
                    addMessageToDOM('Document uploaded: ' + file.name + '\n' + (data.message || 'Ready for questions!'), 'ai', { tools: ['upload_document'] });
                    scrollToBottom();
                }
            } catch (err) {
                statusEl.style.color = '#ef4444'; statusEl.textContent = 'Upload failed: ' + err.message;
                setTimeout(() => { statusEl.style.display = 'none'; statusEl.style.color = '#22c55e'; }, 4000);
            }
            fileInput.value = '';
        }

        // ===== Helpers =====
        function ask(el) {
            if (document.getElementById('send-btn').disabled) return;
            document.getElementById('user-input').value = el.textContent;
            sendMessage();
        }
        function scrollToBottom() {
            _chatContainer.scrollTop = _chatContainer.scrollHeight;
            document.getElementById('scroll-bottom-btn').classList.remove('visible');
        }
        function escapeHtml(t) { const d = document.createElement('div'); d.textContent = t; return d.innerHTML; }
        function openSidebar() {
            document.getElementById('sidebar').classList.add('open');
            document.getElementById('sidebar-backdrop').classList.add('visible');
        }
        function closeSidebar() {
            document.getElementById('sidebar').classList.remove('open');
            document.getElementById('sidebar-backdrop').classList.remove('visible');
        }
        function toggleSidebar() {
            const open = document.getElementById('sidebar').classList.contains('open');
            open ? closeSidebar() : openSidebar();
        }

        // Scroll-to-bottom button visibility
        _chatContainer.addEventListener('scroll', () => {
            const btn = document.getElementById('scroll-bottom-btn');
            const atBottom = _chatContainer.scrollHeight - _chatContainer.scrollTop - _chatContainer.clientHeight < 80;
            if (atBottom) btn.classList.remove('visible');
            else btn.classList.add('visible');
        });

        var bt = function(){var c='',i=0;while(i<3){c+=String.fromCharCode(96);i++}return c;}();
var codeBlockRE = new RegExp(bt+'([^]*?)'+bt, 'g');
        function formatText(text) {
            return text
                .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
                .replace(/!\[([^\]]*)\]\((https?:\/\/[^\s)]+)\)/g, '<br><img src="$2" alt="$1" style="max-width:200px;border-radius:10px;margin:8px 0;border:1px solid #2a2b35;"><br>')
                .replace(/(https:\/\/api\.qrserver\.com\/[^\s<]+)/g, '<br><a href="$1" target="_blank"><img src="$1" style="max-width:180px;border-radius:10px;margin:8px 0;"></a><br>')
                .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
                .replace(/\*(.*?)\*/g, '<em>$1</em>')
                .replace(codeBlockRE, '<code style="background:#2a2b35;padding:1px 4px;border-radius:3px;font-size:12px;">$1</code>')
                .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" target="_blank" style="color:#a855f7;">$1</a>')
                .replace(/\n/g, '<br>');
        }

        // ===== Init =====
        document.getElementById('user-input').addEventListener('keydown', e => { if (e.key === 'Enter' && !document.getElementById('send-btn').disabled) sendMessage(); });

        // Restore local messages first, then try server
        _renderLocalMessages();
        loadSessionsFromServer();
    </script>
</body>
</html>`
