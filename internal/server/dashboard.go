package server

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>MCP Gateway Dashboard</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }

        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #0f1117;
            color: #e4e4e7;
            min-height: 100vh;
        }

        .header {
            background: #1a1b23;
            border-bottom: 1px solid #2a2b35;
            padding: 16px 32px;
            display: flex;
            align-items: center;
            justify-content: space-between;
        }

        .header h1 { font-size: 20px; font-weight: 600; color: #fff; }

        .header .status {
            display: flex; align-items: center; gap: 8px;
            font-size: 13px; color: #a1a1aa;
        }

        .header .dot {
            width: 8px; height: 8px; border-radius: 50%;
            background: #22c55e; animation: pulse 2s infinite;
        }

        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }

        .container { max-width: 1200px; margin: 0 auto; padding: 24px; }

        .stats-grid {
            display: grid; grid-template-columns: repeat(4, 1fr);
            gap: 16px; margin-bottom: 24px;
        }

        .stat-card {
            background: #1a1b23; border: 1px solid #2a2b35;
            border-radius: 12px; padding: 20px;
        }

        .stat-card .label {
            font-size: 12px; color: #71717a;
            text-transform: uppercase; letter-spacing: 0.5px; margin-bottom: 8px;
        }

        .stat-card .value { font-size: 28px; font-weight: 700; color: #fff; }
        .stat-card .value.green { color: #22c55e; }
        .stat-card .value.blue { color: #3b82f6; }
        .stat-card .value.purple { color: #a855f7; }
        .stat-card .value.orange { color: #f97316; }

        /* Try It Section */
        .try-it {
            background: #1a1b23; border: 1px solid #2a2b35;
            border-radius: 12px; padding: 24px; margin-bottom: 24px;
        }

        .try-it h2 {
            font-size: 14px; font-weight: 600; color: #a1a1aa;
            margin-bottom: 16px; text-transform: uppercase; letter-spacing: 0.5px;
        }

        .try-tabs {
            display: flex; gap: 4px; margin-bottom: 16px;
            border-bottom: 1px solid #2a2b35; padding-bottom: 12px;
        }

        .try-tab {
            padding: 8px 16px; border-radius: 8px; border: none;
            background: transparent; color: #71717a; cursor: pointer;
            font-size: 13px; font-weight: 500; transition: all 0.2s;
        }

        .try-tab:hover { color: #e4e4e7; background: #2a2b35; }
        .try-tab.active { color: #a855f7; background: #a855f720; }

        .try-form { display: none; }
        .try-form.active { display: block; }

        .form-row {
            display: flex; gap: 12px; align-items: flex-end; margin-bottom: 12px;
        }

        .form-group { flex: 1; }

        .form-group label {
            display: block; font-size: 12px; color: #71717a;
            margin-bottom: 6px; text-transform: uppercase;
        }

        .form-group input, .form-group select {
            width: 100%; padding: 10px 14px; border-radius: 8px;
            border: 1px solid #2a2b35; background: #0f1117;
            color: #e4e4e7; font-size: 14px; outline: none;
            transition: border-color 0.2s;
        }

        .form-group input:focus, .form-group select:focus {
            border-color: #a855f7;
        }

        .form-group input::placeholder { color: #52525b; }

        .send-btn {
            padding: 10px 24px; border-radius: 8px; border: none;
            background: #a855f7; color: #fff; cursor: pointer;
            font-size: 14px; font-weight: 500; transition: all 0.2s;
            white-space: nowrap;
        }

        .send-btn:hover { background: #9333ea; }
        .send-btn:active { transform: scale(0.95); }
        .send-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        .try-result {
            margin-top: 16px; padding: 16px; border-radius: 8px;
            background: #0f1117; border: 1px solid #2a2b35;
            font-family: 'SF Mono', 'Fira Code', monospace;
            font-size: 13px; color: #a1a1aa;
            white-space: pre-wrap; max-height: 250px;
            overflow-y: auto; display: none; line-height: 1.5;
        }

        .try-result.success { border-color: #22c55e40; }
        .try-result.error { border-color: #ef444440; }

        /* Panels */
        .panels {
            display: grid; grid-template-columns: 1fr 1fr;
            gap: 16px; margin-bottom: 24px;
        }

        .panel {
            background: #1a1b23; border: 1px solid #2a2b35;
            border-radius: 12px; padding: 20px;
        }

        .panel h2 {
            font-size: 14px; font-weight: 600; color: #a1a1aa;
            margin-bottom: 16px; text-transform: uppercase; letter-spacing: 0.5px;
        }

        .server-item {
            display: flex; align-items: center; justify-content: space-between;
            padding: 12px; border-radius: 8px; margin-bottom: 8px; background: #0f1117;
        }

        .server-item .left { display: flex; align-items: center; gap: 12px; }

        .server-item .status-dot { width: 10px; height: 10px; border-radius: 50%; }
        .server-item .status-dot.online { background: #22c55e; }
        .server-item .status-dot.offline { background: #ef4444; }
        .server-item .status-dot.unknown { background: #71717a; }

        .server-item .name { font-weight: 500; color: #fff; }
        .server-item .meta { font-size: 12px; color: #71717a; }

        .tool-item {
            display: flex; align-items: center; justify-content: space-between;
            padding: 10px 12px; border-radius: 8px; margin-bottom: 6px; background: #0f1117;
        }

        .tool-item .name {
            font-family: 'SF Mono', 'Fira Code', monospace;
            font-size: 13px; color: #a855f7;
        }

        .tool-item .desc {
            font-size: 11px; color: #71717a; max-width: 300px;
            white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
        }

        .tool-item .server-badge {
            font-size: 11px; padding: 2px 8px; border-radius: 4px;
            background: #2a2b35; color: #a1a1aa;
        }

        /* Logs */
        .logs-panel {
            background: #1a1b23; border: 1px solid #2a2b35;
            border-radius: 12px; padding: 20px;
        }

        .logs-panel h2 {
            font-size: 14px; font-weight: 600; color: #a1a1aa;
            margin-bottom: 16px; text-transform: uppercase; letter-spacing: 0.5px;
        }

        .log-entry {
            display: grid; grid-template-columns: 80px 100px 140px 80px 1fr;
            gap: 12px; padding: 10px 12px; border-radius: 6px; margin-bottom: 4px;
            font-size: 13px; font-family: 'SF Mono', 'Fira Code', monospace;
        }

        .log-entry:nth-child(odd) { background: #0f1117; }
        .log-entry .time { color: #71717a; }
        .log-entry .method { color: #3b82f6; }
        .log-entry .tool { color: #a855f7; }
        .log-entry .latency { color: #f97316; }
        .log-entry .status-success { color: #22c55e; }
        .log-entry .status-error { color: #ef4444; }

        .empty-state { text-align: center; padding: 40px; color: #71717a; font-size: 14px; }

        /* Auth Overlay */
        .auth-overlay {
            position: fixed; inset: 0; background: rgba(0,0,0,0.8);
            display: flex; align-items: center; justify-content: center;
            z-index: 1000; backdrop-filter: blur(4px);
        }
        .auth-overlay.hidden { display: none; }

        .auth-box {
            background: #1a1b23; border: 1px solid #2a2b35;
            border-radius: 16px; padding: 40px; width: 400px;
            max-width: 90vw;
        }

        .auth-box h2 {
            font-size: 22px; font-weight: 700; color: #fff;
            margin-bottom: 8px;
        }

        .auth-box .subtitle {
            font-size: 14px; color: #71717a; margin-bottom: 24px;
        }

        .auth-box .form-group {
            margin-bottom: 16px;
        }

        .auth-box .form-group label {
            display: block; font-size: 12px; color: #71717a;
            margin-bottom: 6px; text-transform: uppercase;
        }

        .auth-box .form-group input {
            width: 100%; padding: 12px 14px; border-radius: 8px;
            border: 1px solid #2a2b35; background: #0f1117;
            color: #e4e4e7; font-size: 14px; outline: none;
        }

        .auth-box .form-group input:focus {
            border-color: #a855f7;
        }

        .auth-btn {
            width: 100%; padding: 12px; border-radius: 8px; border: none;
            background: #a855f7; color: #fff; cursor: pointer;
            font-size: 15px; font-weight: 600; transition: all 0.2s;
            margin-top: 8px;
        }

        .auth-btn:hover { background: #9333ea; }
        .auth-btn:disabled { opacity: 0.5; cursor: not-allowed; }

        .auth-switch {
            text-align: center; margin-top: 16px;
            font-size: 13px; color: #71717a;
        }

        .auth-switch a {
            color: #a855f7; cursor: pointer; text-decoration: none;
        }

        .auth-switch a:hover { text-decoration: underline; }

        .auth-error {
            background: #ef444420; border: 1px solid #ef444440;
            color: #ef4444; padding: 10px 14px; border-radius: 8px;
            font-size: 13px; margin-bottom: 16px; display: none;
        }

        .auth-success {
            background: #22c55e20; border: 1px solid #22c55e40;
            color: #22c55e; padding: 10px 14px; border-radius: 8px;
            font-size: 13px; margin-bottom: 16px; display: none;
        }

        .auth-user {
            display: flex; align-items: center; gap: 12px;
        }

        .auth-user .avatar {
            width: 28px; height: 28px; border-radius: 50%;
            background: #a855f7; color: #fff;
            display: flex; align-items: center; justify-content: center;
            font-size: 12px; font-weight: 700;
        }

        .auth-user .uname { font-size: 13px; color: #e4e4e7; }

        .logout-btn {
            padding: 6px 14px; border-radius: 6px; border: 1px solid #2a2b35;
            background: transparent; color: #71717a; cursor: pointer;
            font-size: 12px; transition: all 0.2s;
        }

        .logout-btn:hover { color: #ef4444; border-color: #ef444440; }

        /* Password show/hide toggle */
        .pw-wrapper { position: relative; }
        .pw-wrapper input { padding-right: 40px !important; }
        .pw-toggle {
            position: absolute; right: 10px; top: 50%; transform: translateY(-50%);
            background: none; border: none; cursor: pointer; color: #71717a; padding: 0;
            display: flex; align-items: center;
        }
        .pw-toggle:hover { color: #a855f7; }
        .pw-toggle svg { width: 16px; height: 16px; }

        /* Auth logo */
        .auth-logo {
            width: 48px; height: 48px; border-radius: 12px; background: #a855f720;
            border: 1px solid #a855f740; display: flex; align-items: center; justify-content: center;
            margin: 0 auto 20px; color: #a855f7;
        }
        .auth-logo svg { width: 24px; height: 24px; }

        /* Auth button loading spinner */
        .auth-btn .spinner {
            display: none; width: 16px; height: 16px; border: 2px solid rgba(255,255,255,0.3);
            border-top-color: #fff; border-radius: 50%; animation: spin 0.7s linear infinite;
            margin: 0 auto;
        }
        .auth-btn.loading .btn-text { display: none; }
        .auth-btn.loading .spinner { display: block; }
        @keyframes spin { to { transform: rotate(360deg); } }

        /* Stat card subtitle */
        .stat-card .subtitle { font-size: 11px; color: #52525b; margin-top: 4px; }

        @media (max-width: 768px) {
            .stats-grid { grid-template-columns: repeat(2, 1fr); }
            .panels { grid-template-columns: 1fr; }
            .log-entry { grid-template-columns: 1fr; gap: 4px; }
            .form-row { flex-direction: column; }
            .header { padding: 12px 16px; }
            .try-tabs { overflow-x: auto; flex-wrap: nowrap; -webkit-overflow-scrolling: touch; scrollbar-width: none; }
            .try-tabs::-webkit-scrollbar { display: none; }
        }
    </style>
</head>
<body>
    <!-- Auth Overlay -->
    <div class="auth-overlay" id="authOverlay">
        <div class="auth-box">
            <div id="authSignupForm">
                <div class="auth-logo">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M13.5 10.5V6.75a4.5 4.5 0 119 0v3.75M3.75 21.75h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H3.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" /></svg>
                </div>
                <h2>Create Account</h2>
                <p class="subtitle">Sign up to use the MCP Gateway</p>
                <div class="auth-error" id="authError"></div>
                <div class="auth-success" id="authSuccess"></div>
                <div class="form-group">
                    <label>Username</label>
                    <input type="text" id="signupUsername" placeholder="Choose a username" onkeydown="if(event.key==='Enter')handleSignup()" />
                </div>
                <div class="form-group">
                    <label>Email</label>
                    <input type="email" id="signupEmail" placeholder="you@example.com" onkeydown="if(event.key==='Enter')handleSignup()" />
                </div>
                <div class="form-group">
                    <label>Password</label>
                    <div class="pw-wrapper">
                        <input type="password" id="signupPassword" placeholder="At least 6 characters" onkeydown="if(event.key==='Enter')handleSignup()" />
                        <button class="pw-toggle" type="button" onclick="togglePw('signupPassword',this)" tabindex="-1">
                            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
                        </button>
                    </div>
                </div>
                <button class="auth-btn" id="signupBtn" onclick="handleSignup()"><span class="btn-text">Sign Up</span><div class="spinner"></div></button>
                <div class="auth-switch">Already have an account? <a onclick="showLogin()">Log in</a></div>
            </div>

            <div id="authLoginForm" style="display:none;">
                <div class="auth-logo">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M13.5 10.5V6.75a4.5 4.5 0 119 0v3.75M3.75 21.75h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H3.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" /></svg>
                </div>
                <h2>Welcome Back</h2>
                <p class="subtitle">Log in to continue using the MCP Gateway</p>
                <div class="auth-error" id="loginError"></div>
                <div class="form-group">
                    <label>Username</label>
                    <input type="text" id="loginUsername" placeholder="Your username" onkeydown="if(event.key==='Enter')handleLogin()" />
                </div>
                <div class="form-group">
                    <label>Password</label>
                    <div class="pw-wrapper">
                        <input type="password" id="loginPassword" placeholder="Your password" onkeydown="if(event.key==='Enter')handleLogin()" />
                        <button class="pw-toggle" type="button" onclick="togglePw('loginPassword',this)" tabindex="-1">
                            <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
                        </button>
                    </div>
                </div>
                <button class="auth-btn" id="loginBtn" onclick="handleLogin()"><span class="btn-text">Log In</span><div class="spinner"></div></button>
                <div class="auth-switch">Don't have an account? <a onclick="showSignup()">Sign up</a></div>
            </div>
        </div>
    </div>

    <div class="header">
        <h1>MCP Gateway</h1>
        <div class="status">
            <a href="/chat" style="color:#a855f7;text-decoration:none;margin-right:16px;font-size:13px;">AI Chat →</a>
            <div id="userInfo" style="display:none;" class="auth-user">
                <div class="avatar" id="userAvatar">U</div>
                <span class="uname" id="userName">user</span>
                <button class="logout-btn" onclick="handleLogout()">Logout</button>
            </div>
            <div class="dot"></div>
            <span id="header-status"></span>
        </div>
    </div>

    <div class="container" style="display:none;">
        <!-- Stats -->
        <div class="stats-grid">
            <div class="stat-card">
                <div class="label">Total Requests</div>
                <div class="value blue" id="stat-total">0</div>
                <div class="subtitle">All-time MCP tool calls</div>
            </div>
            <div class="stat-card">
                <div class="label">Servers Online</div>
                <div class="value green" id="stat-servers">0</div>
                <div class="subtitle">Active / configured</div>
            </div>
            <div class="stat-card">
                <div class="label">Tools Available</div>
                <div class="value purple" id="stat-tools">0</div>
                <div class="subtitle">Across all servers</div>
            </div>
            <div class="stat-card">
                <div class="label">Avg Latency</div>
                <div class="value orange" id="stat-latency">0ms</div>
                <div class="subtitle">Per tool call response time</div>
            </div>
        </div>

        <!-- Try It Live -->
        <div class="try-it">
            <h2>Try It Live</h2>

            <div class="try-tabs">
                <button class="try-tab active" onclick="switchTab('weather', event)">Weather</button>
                <button class="try-tab" onclick="switchTab('github', event)">GitHub</button>
                <button class="try-tab" onclick="switchTab('notes', event)">Notes</button>
                <button class="try-tab" onclick="switchTab('crypto', event)">Crypto</button>
                <button class="try-tab" onclick="switchTab('news', event)">News</button>
                <button class="try-tab" onclick="switchTab('url', event)">URL Tools</button>
                <button class="try-tab" onclick="switchTab('search', event)">Search</button>
                <button class="try-tab" onclick="switchTab('docs', event)">Documents</button>
            </div>

            <!-- Weather Form -->
            <div class="try-form active" id="form-weather">
                <div class="form-row">
                    <div class="form-group">
                        <label>City Name</label>
                        <input type="text" id="weather-city" placeholder="Enter any city... (e.g., Paris, Mumbai, New York)" />
                    </div>
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Type</label>
                        <select id="weather-type">
                            <option value="get_weather">Current Weather</option>
                            <option value="get_forecast">3-Day Forecast</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callWeather()">Get Weather</button>
                </div>
            </div>

            <!-- GitHub Form -->
            <div class="try-form" id="form-github">
                <div class="form-row">
                    <div class="form-group">
                        <label>Username or Owner</label>
                        <input type="text" id="github-user" placeholder="Enter any GitHub username... (e.g., torvalds, google)" />
                    </div>
                    <div class="form-group">
                        <label>Repo Name (optional, for repo details)</label>
                        <input type="text" id="github-repo" placeholder="e.g., react, linux, vscode" />
                    </div>
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="github-action">
                            <option value="get_user">User Profile</option>
                            <option value="list_repos">List Repos</option>
                            <option value="get_repo">Repo Details</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callGithub()">Fetch</button>
                </div>
            </div>

            <!-- Notes Form -->
            <div class="try-form" id="form-notes">
                <div class="form-row">
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="notes-action" onchange="toggleNoteFields()">
                            <option value="add_note">Add Note</option>
                            <option value="list_notes">List Notes</option>
                            <option value="search_notes">Search Notes</option>
                        </select>
                    </div>
                    <div class="form-group" id="note-title-group">
                        <label>Title</label>
                        <input type="text" id="note-title" placeholder="Note title..." />
                    </div>
                    <div class="form-group" id="note-content-group">
                        <label>Content / Search Query</label>
                        <input type="text" id="note-content" placeholder="Note content or search keyword..." />
                    </div>
                    <button class="send-btn" onclick="callNotes()">Send</button>
                </div>
            </div>

            <!-- Crypto Form -->
            <div class="try-form" id="form-crypto">
                <div class="form-row">
                    <div class="form-group">
                        <label>Coin Name</label>
                        <input type="text" id="crypto-coin" placeholder="Enter coin... (e.g., bitcoin, ethereum, solana, dogecoin)" />
                    </div>
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="crypto-action">
                            <option value="get_crypto_price">Get Price</option>
                            <option value="get_top_cryptos">Top 10</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callCrypto()">Fetch</button>
                </div>
            </div>

            <!-- News Form -->
            <div class="try-form" id="form-news">
                <div class="form-row">
                    <div class="form-group">
                        <label>Search / Topic</label>
                        <input type="text" id="news-query" placeholder="Search news... (e.g., AI, SpaceX, climate change)" />
                    </div>
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="news-action">
                            <option value="search_news">Search News</option>
                            <option value="get_top_news">Top Headlines</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callNews()">Fetch</button>
                </div>
            </div>

            <!-- URL Tools Form -->
            <div class="try-form" id="form-url">
                <div class="form-row">
                    <div class="form-group">
                        <label>URL or Text</label>
                        <input type="text" id="url-input" placeholder="Enter a URL or text... (e.g., https://github.com)" />
                    </div>
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="url-action">
                            <option value="shorten_url">Shorten URL</option>
                            <option value="generate_qr">Generate QR</option>
                            <option value="expand_url">Expand URL</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callURL()">Go</button>
                </div>
            </div>

            <!-- Search Form -->
            <div class="try-form" id="form-search">
                <div class="form-row">
                    <div class="form-group">
                        <label>Search Query</label>
                        <input type="text" id="search-query" placeholder="Search anything... (e.g., Messi goals, population of India)" />
                    </div>
                    <div class="form-group" style="flex: 0 0 160px;">
                        <label>Source</label>
                        <select id="search-source">
                            <option value="web_search">Web Search</option>
                            <option value="wikipedia_summary">Wikipedia</option>
                        </select>
                    </div>
                    <button class="send-btn" onclick="callSearch()">Search</button>
                </div>
            </div>

            <!-- Documents (RAG) Form -->
            <div class="try-form" id="form-docs">
                <div class="form-row">
                    <div class="form-group" style="flex: 0 0 150px;">
                        <label>Action</label>
                        <select id="docs-action" onchange="toggleDocFields()">
                            <option value="ask_document">Ask Question</option>
                            <option value="upload_document">Upload Doc</option>
                            <option value="list_documents">List Docs</option>
                        </select>
                    </div>
                    <div class="form-group" id="doc-question-group">
                        <label>Question</label>
                        <input type="text" id="doc-question" placeholder="Ask a question about your docs..." />
                    </div>
                    <div class="form-group" id="doc-upload-group" style="display:none;">
                        <label>Choose File (.txt, .md, .pdf text)</label>
                        <div style="display:flex;gap:8px;align-items:center;">
                            <input type="file" id="doc-file" accept=".txt,.md,.csv,.json,.py,.js,.go,.html" style="font-size:12px;color:#a1a1aa;flex:1;" onchange="handleFileSelect()" />
                            <input type="text" id="doc-name" placeholder="Doc name" style="width:140px;" />
                        </div>
                    </div>
                    <div class="form-group" id="doc-paste-group" style="display:none;">
                        <label>Or paste text directly</label>
                        <textarea id="doc-paste" rows="2" placeholder="Paste document content here..." style="width:100%;padding:8px 12px;border-radius:8px;border:1px solid #2a2b35;background:#0f1117;color:#e4e4e7;font-size:13px;resize:vertical;font-family:inherit;"></textarea>
                    </div>
                    <button class="send-btn" onclick="callDocs()">Go</button>
                </div>
            </div>

            <div class="try-result" id="try-result"></div>
        </div>

        <!-- Servers & Tools -->
        <div class="panels">
            <div class="panel">
                <h2>Connected Servers</h2>
                <div id="servers-list"><div class="empty-state">Loading...</div></div>
            </div>
            <div class="panel">
                <h2>Available Tools</h2>
                <div id="tools-list"><div class="empty-state">Loading...</div></div>
            </div>
        </div>

        <!-- Request Log -->
        <div class="logs-panel">
            <h2>Recent Requests</h2>
            <div id="logs-list">
                <div class="empty-state">No requests yet. Try the tools above!</div>
            </div>
        </div>
    </div>

    <script>
        const API_BASE = window.location.origin;

        // --- Auth ---
        function getToken() { return localStorage.getItem('mcp_token'); }
        function setToken(t) { localStorage.setItem('mcp_token', t); }
        function clearToken() { localStorage.removeItem('mcp_token'); }

        function authHeaders() {
            const t = getToken();
            return t ? { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + t } : { 'Content-Type': 'application/json' };
        }

        function apiFetch(url, opts) {
            const h = authHeaders();
            if (opts && opts.headers) Object.assign(h, opts.headers);
            return fetch(url, { ...opts, headers: h }).then(r => {
                if (r.status === 401 && getToken()) {
                    clearToken();
                    showAuthOverlay();
                    throw new Error('Session expired');
                }
                return r.json();
            });
        }

        function handleAuthResponse(data) {
            if (data.token) {
                setToken(data.token);
                hideAuthOverlay();
                const u = data.username || localStorage.getItem('mcp_username');
                setUserDisplay(u);
                refreshAll();
                startRefreshInterval();
            }
        }

        function showAuthOverlay() {
            document.getElementById('authOverlay').classList.remove('hidden');
            document.querySelector('.container').style.display = 'none';
        }

        function hideAuthOverlay() {
            document.getElementById('authOverlay').classList.add('hidden');
            document.querySelector('.container').style.display = 'block';
        }

        function showLogin() {
            document.getElementById('authSignupForm').style.display = 'none';
            document.getElementById('authLoginForm').style.display = 'block';
            document.getElementById('loginError').style.display = 'none';
        }

        function showSignup() {
            document.getElementById('authSignupForm').style.display = 'block';
            document.getElementById('authLoginForm').style.display = 'none';
            document.getElementById('authError').style.display = 'none';
        }

        function setUserDisplay(username) {
            const info = document.getElementById('userInfo');
            info.style.display = 'flex';
            document.getElementById('userAvatar').textContent = username.charAt(0).toUpperCase();
            document.getElementById('userName').textContent = username;
            localStorage.setItem('mcp_username', username);
        }

        async function handleSignup() {
            const username = document.getElementById('signupUsername').value.trim();
            const email = document.getElementById('signupEmail').value.trim();
            const password = document.getElementById('signupPassword').value;
            const errEl = document.getElementById('authError');
            const sucEl = document.getElementById('authSuccess');
            const btn = document.getElementById('signupBtn');
            errEl.style.display = 'none';
            sucEl.style.display = 'none';

            if (!username || !email || !password) {
                errEl.textContent = 'All fields are required';
                errEl.style.display = 'block';
                return;
            }

            btn.classList.add('loading'); btn.disabled = true;
            try {
                const resp = await fetch(API_BASE + '/api/auth/signup', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username, email, password })
                });
                const data = await resp.json();
                if (resp.ok) {
                    sucEl.textContent = 'Account created! Logging in...';
                    sucEl.style.display = 'block';
                    setTimeout(() => handleAuthResponse(data), 500);
                } else {
                    errEl.textContent = data.error || 'Signup failed';
                    errEl.style.display = 'block';
                    btn.classList.remove('loading'); btn.disabled = false;
                }
            } catch (e) {
                errEl.textContent = 'Connection error: ' + e.message;
                errEl.style.display = 'block';
                btn.classList.remove('loading'); btn.disabled = false;
            }
        }

        async function handleLogin() {
            const username = document.getElementById('loginUsername').value.trim();
            const password = document.getElementById('loginPassword').value;
            const errEl = document.getElementById('loginError');
            const btn = document.getElementById('loginBtn');
            errEl.style.display = 'none';

            if (!username || !password) {
                errEl.textContent = 'All fields are required';
                errEl.style.display = 'block';
                return;
            }

            btn.classList.add('loading'); btn.disabled = true;
            try {
                const resp = await fetch(API_BASE + '/api/auth/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ username, password })
                });
                const data = await resp.json();
                if (resp.ok) {
                    handleAuthResponse(data);
                } else {
                    errEl.textContent = data.error || 'Login failed';
                    errEl.style.display = 'block';
                    btn.classList.remove('loading'); btn.disabled = false;
                }
            } catch (e) {
                errEl.textContent = 'Connection error: ' + e.message;
                errEl.style.display = 'block';
                btn.classList.remove('loading'); btn.disabled = false;
            }
        }

        function handleLogout() {
            clearToken();
            localStorage.removeItem('mcp_username');
            // Clear all chat data so the next user doesn't see this user's history
            localStorage.removeItem('chat_messages');
            localStorage.removeItem('local_sessions');
            localStorage.removeItem('local_session_id');
            // Stop the refresh interval so it doesn't fire after logout
            if (refreshInterval) { clearInterval(refreshInterval); refreshInterval = null; }
            document.getElementById('userInfo').style.display = 'none';
            showAuthOverlay();
        }

        // Check auth on load — start refresh interval only after auth is confirmed
        let refreshInterval = null;
        function startRefreshInterval() {
            if (!refreshInterval) {
                refreshInterval = setInterval(refreshAll, 5000);
            }
        }

        const savedToken = getToken();
        if (savedToken) {
            fetch(API_BASE + '/api/auth/me', { headers: { 'Authorization': 'Bearer ' + savedToken } })
                .then(r => {
                    if (r.ok) {
                        hideAuthOverlay();
                        const uname = localStorage.getItem('mcp_username');
                        if (uname) setUserDisplay(uname);
                        refreshAll();
                        startRefreshInterval();
                    } else if (r.status === 503) {
                        clearToken();
                        hideAuthOverlay();
                        refreshAll();
                        startRefreshInterval();
                    } else {
                        clearToken();
                        showAuthOverlay();
                    }
                })
                .catch(() => { showAuthOverlay(); });
        } else {
            // No saved token — check if auth is even configured
            fetch(API_BASE + '/api/auth/me')
                .then(r => {
                    if (r.status === 503) {
                        // Auth disabled — skip login
                        hideAuthOverlay();
                        refreshAll();
                        startRefreshInterval();
                    } else {
                        showAuthOverlay();
                    }
                })
                .catch(() => { showAuthOverlay(); });
        }

        // XSS escape helper
        function esc(s) { const d = document.createElement('div'); d.textContent = String(s || ''); return d.innerHTML; }

        // Password show/hide toggle
        function togglePw(inputId, btn) {
            const inp = document.getElementById(inputId);
            inp.type = inp.type === 'password' ? 'text' : 'password';
        }

        // --- Tab Switching ---
        function switchTab(tab, e) {
            document.querySelectorAll('.try-tab').forEach(t => t.classList.remove('active'));
            document.querySelectorAll('.try-form').forEach(f => f.classList.remove('active'));
            if (e && e.target) e.target.classList.add('active');
            document.getElementById('form-' + tab).classList.add('active');
        }

        function toggleNoteFields() {
            const action = document.getElementById('notes-action').value;
            const titleGroup = document.getElementById('note-title-group');
            const contentGroup = document.getElementById('note-content-group');
            const contentInput = document.getElementById('note-content');

            if (action === 'add_note') {
                titleGroup.style.display = 'block';
                contentGroup.style.display = 'block';
                contentInput.placeholder = 'Note content...';
            } else if (action === 'search_notes') {
                titleGroup.style.display = 'none';
                contentGroup.style.display = 'block';
                contentInput.placeholder = 'Search keyword...';
            } else {
                titleGroup.style.display = 'none';
                contentGroup.style.display = 'none';
            }
        }

        // --- Tool Calls ---
        async function callWeather() {
            const city = document.getElementById('weather-city').value.trim();
            if (!city) { showResult('Please enter a city name!', true); return; }
            const tool = document.getElementById('weather-type').value;
            await callTool(tool, { city: city });
        }

        async function callGithub() {
            const user = document.getElementById('github-user').value.trim();
            if (!user) { showResult('Please enter a GitHub username!', true); return; }
            const action = document.getElementById('github-action').value;
            const repo = document.getElementById('github-repo').value.trim();

            if (action === 'get_user') {
                await callTool('get_user', { username: user });
            } else if (action === 'list_repos') {
                await callTool('list_repos', { username: user, sort: 'stars' });
            } else if (action === 'get_repo') {
                if (!repo) { showResult('Please enter a repo name for repo details!', true); return; }
                await callTool('get_repo', { owner: user, repo: repo });
            }
        }

        async function callNotes() {
            const action = document.getElementById('notes-action').value;
            if (action === 'add_note') {
                const title = document.getElementById('note-title').value.trim();
                const content = document.getElementById('note-content').value.trim();
                if (!title || !content) { showResult('Please enter both title and content!', true); return; }
                await callTool('add_note', { title: title, content: content });
            } else if (action === 'search_notes') {
                const query = document.getElementById('note-content').value.trim();
                if (!query) { showResult('Please enter a search keyword!', true); return; }
                await callTool('search_notes', { query: query });
            } else {
                await callTool('list_notes', {});
            }
        }

        async function callCrypto() {
            const action = document.getElementById('crypto-action').value;
            if (action === 'get_top_cryptos') {
                await callTool('get_top_cryptos', {});
            } else {
                const coin = document.getElementById('crypto-coin').value.trim();
                if (!coin) { showResult('Please enter a coin name!', true); return; }
                await callTool('get_crypto_price', { coin: coin.toLowerCase() });
            }
        }

        async function callNews() {
            const action = document.getElementById('news-action').value;
            const query = document.getElementById('news-query').value.trim();
            if (action === 'search_news') {
                if (!query) { showResult('Please enter a search term!', true); return; }
                await callTool('search_news', { query: query });
            } else {
                await callTool('get_top_news', { topic: query || 'general' });
            }
        }

        async function callURL() {
            const action = document.getElementById('url-action').value;
            const input = document.getElementById('url-input').value.trim();
            if (!input) { showResult('Please enter a URL or text!', true); return; }
            if (action === 'shorten_url') {
                await callTool('shorten_url', { url: input });
            } else if (action === 'generate_qr') {
                await callTool('generate_qr', { text: input });
            } else {
                await callTool('expand_url', { url: input });
            }
        }

        async function callSearch() {
            const source = document.getElementById('search-source').value;
            const query = document.getElementById('search-query').value.trim();
            if (!query) { showResult('Please enter a search query!', true); return; }
            if (source === 'web_search') {
                await callTool('web_search', { query: query });
            } else {
                await callTool('wikipedia_summary', { topic: query });
            }
        }

        let uploadedFileContent = '';

        function toggleDocFields() {
            const action = document.getElementById('docs-action').value;
            const questionGroup = document.getElementById('doc-question-group');
            const uploadGroup = document.getElementById('doc-upload-group');
            const pasteGroup = document.getElementById('doc-paste-group');

            questionGroup.style.display = 'none';
            uploadGroup.style.display = 'none';
            pasteGroup.style.display = 'none';

            if (action === 'ask_document') {
                questionGroup.style.display = 'block';
            } else if (action === 'upload_document') {
                uploadGroup.style.display = 'block';
                pasteGroup.style.display = 'block';
            }
        }

        function handleFileSelect() {
            const file = document.getElementById('doc-file').files[0];
            if (!file) return;

            // Auto-fill doc name from filename
            const nameInput = document.getElementById('doc-name');
            if (!nameInput.value) {
                nameInput.value = file.name.replace(/\.[^.]+$/, '');
            }

            // Read file content
            const reader = new FileReader();
            reader.onload = function(e) {
                uploadedFileContent = e.target.result;
                document.getElementById('doc-paste').value = uploadedFileContent.substring(0, 500) + (uploadedFileContent.length > 500 ? '\n... (file loaded, ' + uploadedFileContent.length + ' chars total)' : '');
            };
            reader.readAsText(file);
        }

        async function callDocs() {
            const action = document.getElementById('docs-action').value;
            if (action === 'list_documents') {
                await callTool('list_documents', {});
            } else if (action === 'ask_document') {
                const question = document.getElementById('doc-question').value.trim();
                if (!question) { showResult('Please enter a question!', true); return; }
                await callTool('ask_document', { question: question });
            } else {
                const name = document.getElementById('doc-name').value.trim();
                // Use file content if available, otherwise use pasted text
                const content = uploadedFileContent || document.getElementById('doc-paste').value.trim();
                if (!name) { showResult('Please enter a document name!', true); return; }
                if (!content) { showResult('Please choose a file or paste content!', true); return; }
                await callTool('upload_document', { name: name, content: content });
                uploadedFileContent = '';
            }
        }

        async function callTool(name, args) {
            const resultEl = document.getElementById('try-result');
            resultEl.style.display = 'block';
            resultEl.className = 'try-result';
            resultEl.textContent = 'Calling ' + name + '...';

            try {
                const resp = await fetch(API_BASE + '/mcp/message', {
                    method: 'POST',
                    headers: authHeaders(),
                    body: JSON.stringify({
                        jsonrpc: '2.0',
                        id: Date.now(),
                        method: 'tools/call',
                        params: { name: name, arguments: args }
                    })
                });
                if (resp.status === 401 && getToken()) {
                    clearToken();
                    showAuthOverlay();
                    return;
                }
                const data = await resp.json();
                if (data.result && data.result.content) {
                    resultEl.textContent = data.result.content.map(c => c.text).join('\n');
                    resultEl.classList.add('success');
                } else if (data.error) {
                    resultEl.textContent = 'Error: ' + (typeof data.error === 'string' ? data.error : JSON.stringify(data.error));
                    resultEl.classList.add('error');
                } else {
                    resultEl.textContent = JSON.stringify(data, null, 2);
                }
            } catch (e) {
                resultEl.textContent = 'Error: ' + e.message;
                resultEl.classList.add('error');
            }

            setTimeout(refreshAll, 500);
        }

        function showResult(msg, isError) {
            const el = document.getElementById('try-result');
            el.style.display = 'block';
            el.className = 'try-result' + (isError ? ' error' : '');
            el.textContent = msg;
        }

        // --- Data Refresh ---
        async function refreshAll() {
            try {
                const [serversRes, toolsRes, logsRes, statsRes] = await Promise.all([
                    apiFetch(API_BASE + '/api/servers'),
                    apiFetch(API_BASE + '/api/tools'),
                    apiFetch(API_BASE + '/api/logs'),
                    apiFetch(API_BASE + '/api/stats'),
                ]);
                updateServers(serversRes);
                updateTools(toolsRes);
                updateLogs(logsRes);
                updateStats(statsRes, serversRes, toolsRes);
                document.getElementById('header-status').textContent = 'Live';
            } catch (e) {
                document.getElementById('header-status').textContent = 'Disconnected';
            }
        }

        function updateStats(stats, servers, tools) {
            document.getElementById('stat-total').textContent = stats.total_requests || 0;
            const onlineCount = (servers.servers || []).filter(s => s.Status === 'online').length;
            document.getElementById('stat-servers').textContent = onlineCount + '/' + (servers.count || 0);
            document.getElementById('stat-tools').textContent = tools.count || 0;
            const avgMs = Math.round(stats.avg_latency_ms || 0);
            document.getElementById('stat-latency').textContent = avgMs + 'ms';
        }

        function updateServers(data) {
            const list = document.getElementById('servers-list');
            if (!data.servers || data.servers.length === 0) {
                list.innerHTML = '<div class="empty-state">No servers configured</div>';
                return;
            }
            list.innerHTML = data.servers.map(s => {
                const toolCount = (s.Tools || []).length;
                const latencyMs = Math.round(s.Latency || 0);
                const dotClass = s.Status === 'online' ? 'online' : (s.Status === 'offline' ? 'offline' : 'unknown');
                return '<div class="server-item">' +
                    '<div class="left">' +
                        '<div class="status-dot ' + dotClass + '"></div>' +
                        '<div><div class="name">' + esc(s.Config.Name) + '</div>' +
                        '<div class="meta">' + esc(s.Config.URL) + '</div></div>' +
                    '</div>' +
                    '<div class="meta">' + toolCount + ' tools | ' + latencyMs + 'ms</div>' +
                '</div>';
            }).join('');
        }

        function updateTools(data) {
            const list = document.getElementById('tools-list');
            if (!data.tools || data.tools.length === 0) {
                list.innerHTML = '<div class="empty-state">No tools available</div>';
                return;
            }
            list.innerHTML = data.tools.map(t =>
                '<div class="tool-item">' +
                    '<div><span class="name">' + esc(t.name) + '</span>' +
                    '<div class="desc">' + esc(t.description || '') + '</div></div>' +
                    '<span class="server-badge">' + esc(t.server_name) + '</span>' +
                '</div>'
            ).join('');
        }

        function updateLogs(data) {
            const list = document.getElementById('logs-list');
            if (!data.logs || data.logs.length === 0) {
                list.innerHTML = '<div class="empty-state">No requests yet. Try the tools above!</div>';
                return;
            }
            list.innerHTML = data.logs.map(l => {
                const time = new Date(l.timestamp).toLocaleTimeString();
                const latencyMs = Math.round(l.latency_ms || 0);
                const statusClass = l.status === 'success' ? 'status-success' : 'status-error';
                return '<div class="log-entry">' +
                    '<span class="time">' + esc(time) + '</span>' +
                    '<span class="method">' + esc(l.method) + '</span>' +
                    '<span class="tool">' + esc(l.tool_name || '-') + '</span>' +
                    '<span class="latency">' + latencyMs + 'ms</span>' +
                    '<span class="' + statusClass + '">' + esc(l.status) + '</span>' +
                '</div>';
            }).join('');
        }

        // Allow Enter key to submit forms
        document.getElementById('weather-city').addEventListener('keydown', e => { if(e.key==='Enter') callWeather(); });
        document.getElementById('github-user').addEventListener('keydown', e => { if(e.key==='Enter') callGithub(); });
        document.getElementById('github-repo').addEventListener('keydown', e => { if(e.key==='Enter') callGithub(); });
        document.getElementById('note-content').addEventListener('keydown', e => { if(e.key==='Enter') callNotes(); });
        document.getElementById('news-query').addEventListener('keydown', e => { if(e.key==='Enter') callNews(); });
        document.getElementById('crypto-coin').addEventListener('keydown', e => { if(e.key==='Enter') callCrypto(); });
        document.getElementById('url-input').addEventListener('keydown', e => { if(e.key==='Enter') callURL(); });
        document.getElementById('search-query').addEventListener('keydown', e => { if(e.key==='Enter') callSearch(); });
    </script>
</body>
</html>`
