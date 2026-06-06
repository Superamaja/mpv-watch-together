(function () {
    "use strict";

    // Check if we're on the player page or watch together redirect page
    // Check for page type - Moved to dynamic checks
    function isPlayerPage() {
        return window.location.hash.includes("#/player/");
    }

    function isWatchTogetherPage() {
        return (
            window.location.pathname.includes("/watchtogether") ||
            window.location.hash.includes("#/watchtogether")
        );
    }

    console.log("GUEST: Watch Together Script Loading...");

    // Set flag to indicate userscript is loaded
    window.stremioWatchTogetherLoaded = true;

    // Configuration - CHANGE THIS TO MATCH HOST'S ROOM ID
    let ROOM_ID = "room123"; // Default room ID - can be changed via settings
    const USER_ID = "guest_" + Math.random().toString(36).substr(2, 6);
    let DISPLAY_NAME = "";

    // Firebase credentials are baked in at build time from .env
    const FIREBASE_CONFIG = __DEFAULT_FIREBASE_CONFIG__;
    const LUCIDE_ICONS = __LUCIDE_ICONS__;

    function lucideIcon(iconName, size = 24) {
        const icon =
            LUCIDE_ICONS[iconName] ||
            LUCIDE_ICONS["circle-question-mark"] ||
            "";
        return `<svg viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${icon}</svg>`;
    }

    // Inject shared styles for the settings modal once (hover feedback,
    // animations, backdrop, focus ring). Targets elements by container class so
    // existing inline styles don't need to change.
    function injectPanelStyles() {
        if (document.getElementById("watch-together-styles")) return;
        const style = document.createElement("style");
        style.id = "watch-together-styles";
        style.textContent = `
            .watch-together-settings-popup button {
                transition: filter 0.15s ease, transform 0.1s ease, box-shadow 0.15s ease;
            }
            .watch-together-settings-popup button:hover {
                filter: brightness(1.14);
            }
            .watch-together-settings-popup button:active {
                transform: scale(0.97);
            }
            .watch-together-settings-popup input:focus {
                outline: none;
                border-color: #4CAF50 !important;
                box-shadow: 0 0 0 3px rgba(76, 175, 80, 0.22);
            }
            .watch-together-backdrop {
                position: fixed;
                inset: 0;
                background: rgba(0, 0, 0, 0.55);
                backdrop-filter: blur(3px);
                z-index: 9999;
                animation: wt-fade-in 0.16s ease;
            }
            .watch-together-settings-popup { animation: wt-pop-in 0.18s ease; }
            .wt-toast-container {
                position: fixed;
                top: 80px;
                left: 50%;
                transform: translateX(-50%);
                z-index: 10001;
                display: flex;
                flex-direction: column;
                gap: 8px;
                align-items: center;
                pointer-events: none;
            }
            .wt-toast-container .control-notification {
                background: rgba(0, 0, 0, 0.95);
                color: white;
                padding: 12px 24px;
                border-radius: 8px;
                border: 2px solid #4CAF50;
                font-size: 14px;
                font-weight: 600;
                box-shadow: 0 4px 12px rgba(0, 0, 0, 0.5);
                max-width: 360px;
                text-align: center;
                pointer-events: auto;
                animation: wtToastIn 0.25s ease;
            }
            @keyframes wt-fade-in { from { opacity: 0; } to { opacity: 1; } }
            @keyframes wt-pop-in {
                from { opacity: 0; transform: translate(-50%, -50%) scale(0.96); }
                to   { opacity: 1; transform: translate(-50%, -50%) scale(1); }
            }
            @keyframes wtToastIn { from { opacity: 0; transform: translateY(-12px); } to { opacity: 1; transform: translateY(0); } }
            @keyframes wtToastOut { from { opacity: 1; } to { opacity: 0; transform: translateY(-8px); } }
        `;
        document.head.appendChild(style);
    }

    // Configuration Management
    const CONFIG_STORAGE_KEY = "stremio_watch_together_config";

    // Load configuration from localStorage
    function loadConfig() {
        try {
            const savedConfig = localStorage.getItem(CONFIG_STORAGE_KEY);
            if (savedConfig) {
                const config = JSON.parse(savedConfig);
                if (config.roomId) ROOM_ID = config.roomId;
                if (config.displayName) DISPLAY_NAME = config.displayName;
                console.log("GUEST: Configuration loaded from localStorage");
            }
        } catch (error) {
            console.error("GUEST ERROR: Failed to load configuration:", error);
        }
    }

    // Save configuration to localStorage
    function saveConfig() {
        try {
            const config = {
                roomId: ROOM_ID,
                displayName: DISPLAY_NAME,
                lastUpdated: Date.now(),
            };
            localStorage.setItem(CONFIG_STORAGE_KEY, JSON.stringify(config));
            console.log("GUEST: Configuration saved to localStorage");
        } catch (error) {
            console.error("GUEST ERROR: Failed to save configuration:", error);
        }
    }

    // Clear configuration
    function clearConfig() {
        try {
            localStorage.removeItem(CONFIG_STORAGE_KEY);
            ROOM_ID = "room123";
            DISPLAY_NAME = "";
            console.log("GUEST: Configuration cleared");
        } catch (error) {
            console.error("GUEST ERROR: Failed to clear configuration:", error);
        }
    }

    // Generate and save persistent display name
    function initializeDisplayName() {
        try {
            const savedConfig = localStorage.getItem(CONFIG_STORAGE_KEY);
            if (savedConfig) {
                const config = JSON.parse(savedConfig);
                if (config.displayName && config.displayName.trim() !== "") {
                    DISPLAY_NAME = config.displayName;
                    console.log("GUEST: Loaded display name:", DISPLAY_NAME);
                    return;
                }
            }

            // Generate new username only if none exists
            if (!DISPLAY_NAME || DISPLAY_NAME.trim() === "") {
                DISPLAY_NAME = generateCoolUsername("guest_");
                saveConfig();
                console.log("GUEST: Generated new display name:", DISPLAY_NAME);
            }
        } catch (error) {
            console.error(
                "GUEST ERROR: Failed to initialize display name:",
                error,
            );
            DISPLAY_NAME = generateCoolUsername("guest_");
        }
    }

    // Generate a cool username
    function generateCoolUsername(baseName = "user") {
        const adjectives = [
            "Crimson",
            "Shadow",
            "Mystic",
            "Blaze",
            "Iron",
            "Quantum",
            "Solar",
            "Lunar",
            "Silent",
            "Frozen",
            "Wild",
            "Fierce",
            "Electric",
            "Golden",
            "Nebula",
            "Eternal",
            "Cobalt",
            "Obsidian",
            "Scarlet",
            "Rapid",
            "Stealthy",
            "Stormy",
            "Glacial",
            "Savage",
            "Vivid",
            "Radiant",
            "Grim",
            "Vengeful",
            "Cyber",
            "Infernal",
            "Royal",
            "Azure",
            "Burning",
            "Silent",
            "Venomous",
            "Titanic",
            "Crystalline",
            "Phantom",
            "Nocturnal",
            "Heroic",
            "Galactic",
            "Omega",
            "Prime",
            "Alpha",
            "Enchanted",
            "Magnetic",
            "Vast",
            "Twilight",
            "Echoing",
            "Wicked",
            "Stellar",
        ];

        const nouns = [
            "Phoenix",
            "Dragon",
            "Hunter",
            "Warrior",
            "Spirit",
            "Vortex",
            "Titan",
            "Specter",
            "Wolf",
            "Falcon",
            "Reaper",
            "Samurai",
            "Knight",
            "Golem",
            "Sniper",
            "Rogue",
            "Ninja",
            "Wizard",
            "Beast",
            "Ghost",
            "Lion",
            "Viper",
            "Assassin",
            "Juggernaut",
            "Guardian",
            "Sentinel",
            "Panther",
            "Serpent",
            "Rider",
            "Crusader",
            "Predator",
            "Gladiator",
            "Shadow",
            "Thunder",
            "Storm",
            "Warden",
            "Enigma",
            "Cyclone",
            "Tempest",
            "Marauder",
            "Saber",
            "Paladin",
            "Specter",
            "Hunter",
            "Nomad",
            "Titan",
            "Comet",
            "Raven",
            "Griffin",
            "Blizzard",
        ];

        const randomAdjective =
            adjectives[Math.floor(Math.random() * adjectives.length)];
        const randomNoun = nouns[Math.floor(Math.random() * nouns.length)];
        const randomNumber = Math.floor(Math.random() * 1000);

        let username = `${randomAdjective}${randomNoun}${randomNumber}`;

        if (baseName !== "user") {
            username = `${baseName}${username}`;
        }

        return username.toLowerCase();
    }

    // Global variables
    let app, database, roomRef;
    let watchTogetherEnabled = false;
    let videoElement = null;
    let playPauseButton = null;
    let controlBar = null;
    let watchTogetherButton = null;
    let settingsButton = null;
    let settingsPopup = null;
    let settingsBackdrop = null;
    let settingsEscHandler = null;
    let lastKnownHostState = null;
    let isFollowingHost = false;
    let bufferingObserver = null;
    let urlMonitorInterval = null;
    let beforeUnloadHandler = null;
    let popstateHandler = null;
    let serverTimeUnsubscribe = null;
    let connectionUnsubscribe = null;
    let hostListenerUnsubscribe = null;
    let stateInterval = null;
    let heartbeatInterval = null;
    let videoStateListeners = [];
    let isGuestBuffering = false;
    let lastGuestStateSent = 0;
    let currentControllerId = null;
    let pendingControlRequest = false;
    let requestControlButton = null;
    let isScriptActive = false;
    let isInitializationRunning = false;
    let scriptKilled = false;
    let lastAppliedForceSyncId = null;
    let lastReportedPlaybackState = null;
    let needsInitialSync = false;

    // Seek-lock: snap a synced, non-controlling guest back to the host's position
    // if they try to scrub away. Tracks the host's clock so we can extrapolate the
    // live position between updates.
    const SEEK_LOCK_THRESHOLD = 3; // seconds of deviation tolerated before snapping
    let hostClockTime = null;
    let hostClockSampledAt = 0;
    let hostClockPlaying = false;
    let lastProgrammaticSeekAt = 0;

    // Connection monitoring (Firebase .info/connected)
    let monitoredDatabase = null;
    let wasConnected = null;
    let hasLostConnection = false;

    // Shared clock: Firebase's server-time offset gives every device a common
    // timebase so cross-device timestamps ignore local clock skew. See serverNow().
    let serverTimeOffset = 0;
    let serverTimeMonitored = false;

    function serverNow() {
        return Date.now() + serverTimeOffset;
    }

    async function monitorServerTimeOffset() {
        if (serverTimeMonitored || !database) return;
        serverTimeMonitored = true;
        try {
            const { ref, onValue } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");
            serverTimeUnsubscribe = onValue(
                ref(database, ".info/serverTimeOffset"),
                (snapshot) => {
                    const offset = snapshot.val();
                    if (typeof offset === "number") serverTimeOffset = offset;
                },
            );
        } catch (error) {
            console.error(
                "GUEST ERROR: Failed to monitor server time offset:",
                error,
            );
        }
    }

    async function deleteFirebaseApp() {
        if (!app) return;

        try {
            const { deleteApp } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-app.js");
            await deleteApp(app);
        } catch (error) {
            console.error("GUEST ERROR: Failed to delete Firebase app:", error);
        } finally {
            app = null;
            database = null;
            roomRef = null;
        }
    }

    // Initialize Firebase
    async function initializeFirebase() {
        try {
            const { initializeApp } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-app.js");
            const { getDatabase, ref } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");

            // Disconnect existing app if it exists
            if (app) {
                cleanupRealtimeListeners();
                await deleteFirebaseApp();
            }

            app = initializeApp(FIREBASE_CONFIG);
            database = getDatabase(app);
            roomRef = ref(database, "rooms/" + ROOM_ID);
            serverTimeMonitored = false;

            console.log("GUEST: Firebase initialized for room:", ROOM_ID);
            monitorConnection();
            monitorServerTimeOffset();
            return true;
        } catch (error) {
            console.error(
                "GUEST ERROR: Firebase initialization failed:",
                error,
            );
            return false;
        }
    }

    // Find DOM elements with multiple selectors
    function findDOMElements() {
        console.log("🔍 GUEST: Searching for DOM elements...");

        // Try multiple selectors for video element
        videoElement =
            document.querySelector("video") ||
            document.querySelector('video[class*="video"]') ||
            document.querySelector('video[class*="player"]');

        // Try multiple selectors for control bar
        controlBar =
            document.querySelector(".control-bar-buttons-container-SWhkU") ||
            document.querySelector('[class*="control-bar-buttons"]') ||
            document.querySelector('[class*="control-bar"]') ||
            document.querySelector(".control-bar");

        // Try multiple selectors for play/pause button
        playPauseButton =
            document.querySelector('div[title="Play"], div[title="Pause"]') ||
            document.querySelector('[title="Play"], [title="Pause"]') ||
            document.querySelector(
                'button[title="Play"], button[title="Pause"]',
            );

        console.log("🔍 GUEST: Found elements:", {
            video: !!videoElement,
            controlBar: !!controlBar,
            playPauseButton: !!playPauseButton,
        });

        if (videoElement && controlBar && playPauseButton) {
            console.log("GUEST: All DOM elements found");
            return true;
        }

        console.log("GUEST ERROR: Missing elements:", {
            video: !videoElement,
            controlBar: !controlBar,
            playPauseButton: !playPauseButton,
        });

        return false;
    }

    // Create Watch Together button
    function createWatchTogetherButton() {
        if (watchTogetherButton) {
            watchTogetherButton.remove();
        }

        watchTogetherButton = document.createElement("div");
        watchTogetherButton.className =
            "control-bar-button-FQUsj button-container-zVLH6";
        watchTogetherButton.title = "Sync Off - click to follow host";
        watchTogetherButton.style.cssText = `
            cursor: pointer;
            width: 42px;
            height: 42px;
            min-width: 42px;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fff;
            border: 1px solid rgba(255, 255, 255, 0.22);
            border-radius: 6px;
            background: rgba(10, 12, 16, 0.56);
            box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.08);
            backdrop-filter: blur(8px);
        `;

        watchTogetherButton.innerHTML = lucideIcon("wifi-sync", 22);

        controlBar.appendChild(watchTogetherButton);
        watchTogetherButton.addEventListener("click", toggleWatchTogether);
        updateSyncButtonState();

        console.log("GUEST: Watch Together button created");
    }

    // Create Request Control button
    function createRequestControlButton() {
        if (requestControlButton) {
            requestControlButton.remove();
        }

        requestControlButton = document.createElement("div");
        requestControlButton.className =
            "control-bar-button-FQUsj button-container-zVLH6 request-control-button";
        requestControlButton.title = "Request Playback Control";
        requestControlButton.style.cssText = `
            cursor: pointer;
            width: 42px;
            height: 42px;
            min-width: 42px;
            color: #fff;
            border: 1px solid rgba(255, 255, 255, 0.22);
            border-radius: 6px;
            background: rgba(10, 12, 16, 0.56);
            margin-left: 4px;
            display: flex;
            position: relative;
            overflow: hidden;
            transition: background 0.16s ease, border-color 0.16s ease;
            box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.08);
            backdrop-filter: blur(8px);
            justify-content: center;
            align-items: center;
        `;

        // Add hover and active states
        const style = document.createElement("style");
        style.textContent = `
            .request-control-button:hover {
                border-color: rgba(156, 39, 176, 0.8) !important;
                background: rgba(156, 39, 176, 0.46) !important;
            }
            .request-control-button:active {
                transform: scale(0.98) !important;
            }
            .request-control-button::before {
                content: '';
                position: absolute;
                top: 0;
                left: -100%;
                width: 100%;
                height: 100%;
                background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.1), transparent);
                transition: left 0.6s;
            }
            .request-control-button:hover::before {
                left: 100%;
            }
        `;
        document.head.appendChild(style);

        requestControlButton.innerHTML = lucideIcon("radio-receiver", 22);

        controlBar.appendChild(requestControlButton);
        requestControlButton.addEventListener("click", requestControl);

        console.log("GUEST: Request Control button created");
    }

    // Update Request Control button state
    function updateRequestControlButton() {
        if (!requestControlButton) return;

        if (currentControllerId === USER_ID) {
            // We have control - hide the button
            requestControlButton.style.display = "none";
            requestControlButton.title = "You have control";
        } else if (watchTogetherEnabled) {
            // Show button when not controlling
            requestControlButton.style.display = "flex";
            requestControlButton.title = "Request Playback Control";
        } else {
            requestControlButton.style.display = "none";
        }
    }

    // Create Settings button
    function createSettingsButton() {
        if (settingsButton) {
            settingsButton.remove();
        }

        settingsButton = document.createElement("div");
        settingsButton.className =
            "control-bar-button-FQUsj button-container-zVLH6";
        settingsButton.title = "Watch Together Settings";
        settingsButton.style.cssText = `
            cursor: pointer;
            width: 42px;
            height: 42px;
            min-width: 42px;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fff;
            border: 1px solid rgba(255, 255, 255, 0.22);
            border-radius: 6px;
            background: rgba(10, 12, 16, 0.56);
            box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.08);
            backdrop-filter: blur(8px);
            margin-left: 4px;
        `;

        settingsButton.innerHTML = lucideIcon("settings", 22);

        controlBar.appendChild(settingsButton);
        settingsButton.addEventListener("click", toggleSettingsPopup);

        console.log("GUEST: Settings button created");
    }

    function hideSettingsPopup() {
        if (settingsPopup) {
            settingsPopup.remove();
            settingsPopup = null;
        }
        if (settingsBackdrop) {
            settingsBackdrop.remove();
            settingsBackdrop = null;
        }
        if (settingsEscHandler) {
            document.removeEventListener("keydown", settingsEscHandler);
            settingsEscHandler = null;
        }
    }

    function clearAllSettings() {
        if (
            confirm(
                "Are you sure you want to clear local room and display-name settings?",
            )
        ) {
            clearConfig();
            hideSettingsPopup();
            showHostStatus("Settings cleared - reloading...");
            setTimeout(() => {
                location.reload();
            }, 1200);
        }
    }

    async function killScript() {
        scriptKilled = true;
        isInitializationRunning = false;
        stopFollowingHost();
        cleanup();
        await cleanupRuntimeBindings();
        console.log("GUEST: Script stopped until this userscript runs again");
    }

    function cleanupRealtimeListeners() {
        if (hostListenerUnsubscribe) {
            hostListenerUnsubscribe();
            hostListenerUnsubscribe = null;
        }
        if (connectionUnsubscribe) {
            connectionUnsubscribe();
            connectionUnsubscribe = null;
        }
        if (serverTimeUnsubscribe) {
            serverTimeUnsubscribe();
            serverTimeUnsubscribe = null;
        }

        monitoredDatabase = null;
        serverTimeMonitored = false;
        wasConnected = null;
        hasLostConnection = false;
    }

    async function cleanupRuntimeBindings() {
        cleanupRealtimeListeners();

        if (urlMonitorInterval) {
            clearInterval(urlMonitorInterval);
            urlMonitorInterval = null;
        }
        if (beforeUnloadHandler) {
            window.removeEventListener("beforeunload", beforeUnloadHandler);
            beforeUnloadHandler = null;
        }
        if (popstateHandler) {
            window.removeEventListener("popstate", popstateHandler);
            popstateHandler = null;
        }

        await deleteFirebaseApp();
    }

    function toggleSettingsPopup() {
        if (settingsPopup) {
            hideSettingsPopup();
            return;
        }

        showSettingsPopup();
    }

    async function copySettingsText(value, buttonId) {
        try {
            if (navigator.clipboard && navigator.clipboard.writeText) {
                await navigator.clipboard.writeText(value);
            } else {
                const textArea = document.createElement("textarea");
                textArea.value = value;
                textArea.style.position = "fixed";
                textArea.style.opacity = "0";
                document.body.appendChild(textArea);
                textArea.select();
                document.execCommand("copy");
                textArea.remove();
            }

            const button = document.getElementById(buttonId);
            if (button) {
                const originalText = button.textContent;
                button.textContent = "Copied";
                setTimeout(() => {
                    button.textContent = originalText;
                }, 1600);
            }
        } catch (error) {
            console.error("GUEST ERROR: Failed to copy text:", error);
            alert("Failed to copy. Please copy manually.");
        }
    }

    function showSettingsPopup() {
        hideSettingsPopup();

        settingsBackdrop = document.createElement("div");
        settingsBackdrop.className = "watch-together-backdrop";
        settingsBackdrop.addEventListener("click", hideSettingsPopup);
        document.body.appendChild(settingsBackdrop);

        settingsPopup = document.createElement("div");
        settingsPopup.className = "watch-together-settings-popup";
        settingsPopup.style.cssText = `
            position: fixed;
            top: 50%;
            left: 50%;
            transform: translate(-50%, -50%);
            background: rgba(22, 24, 28, 0.82);
            backdrop-filter: blur(20px) saturate(120%);
            -webkit-backdrop-filter: blur(20px) saturate(120%);
            border: 1px solid rgba(255, 255, 255, 0.12);
            border-radius: 16px;
            padding: 0;
            z-index: 10000;
            width: 420px;
            max-width: 95vw;
            color: white;
            font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
            box-shadow: 0 24px 60px rgba(0, 0, 0, 0.55);
            overflow: hidden;
        `;

        settingsPopup.innerHTML = `
            <div style="background: linear-gradient(135deg, #4CAF50 0%, #45a049 100%); padding: 18px; display: flex; align-items: center; justify-content: space-between; gap: 12px;">
                <div>
                    <h3 style="margin: 0; font-size: 20px; font-weight: 700;">Watch Together Settings</h3>
                    <p style="margin: 6px 0 0 0; opacity: 0.9; font-size: 13px;">Room and identity settings</p>
                </div>
                <button id="closeSettings" title="Close Settings" aria-label="Close Settings" style="background: rgba(0,0,0,0.2); border: none; color: white; width: 34px; height: 34px; border-radius: 6px; cursor: pointer; display: flex; align-items: center; justify-content: center; font-size: 20px; line-height: 1;">&times;</button>
            </div>
            <div style="padding: 20px;">
                <label style="display: block; margin-bottom: 8px; font-weight: 600; color: #e0e0e0;">Display Name</label>
                <input type="text" id="displayNameInput" value="${DISPLAY_NAME || ""}" style="width: 100%; box-sizing: border-box; margin-bottom: 16px; padding: 12px; border: 2px solid #444; border-radius: 8px; background: #2a2a2a; color: white; font-size: 14px;">

                <label style="display: block; margin-bottom: 8px; font-weight: 600; color: #e0e0e0;">Room ID</label>
                <div style="display: flex; gap: 8px; margin-bottom: 20px;">
                    <input type="text" id="roomIdInput" value="${ROOM_ID}" style="flex: 1; min-width: 0; padding: 12px; border: 2px solid #444; border-radius: 8px; background: #2a2a2a; color: white; font-size: 14px;">
                    <button id="copyRoomId" title="Copy Room ID" style="width: 46px; border: none; border-radius: 8px; background: #444; color: white; cursor: pointer; display: flex; align-items: center; justify-content: center;">${lucideIcon("clipboard-copy", 20)}</button>
                </div>

                <div style="display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap;">
                    <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                        <button id="killScript" title="Stop Watch Together until the userscript runs again" style="padding: 11px 14px; background: #8b1e1e; color: white; border: none; border-radius: 8px; cursor: pointer; font-size: 13px; font-weight: 700;">Stop Script</button>
                        <button id="clearConfig" style="padding: 11px 14px; background: #666; color: white; border: none; border-radius: 8px; cursor: pointer; font-size: 13px; font-weight: 600;">Clear Local</button>
                    </div>
                    <div style="display: flex; gap: 10px; flex-wrap: wrap; margin-left: auto;">
                        <button id="cancelSettings" style="padding: 11px 14px; background: #666; color: white; border: none; border-radius: 8px; cursor: pointer; font-size: 13px; font-weight: 600;">Cancel</button>
                        <button id="saveSettings" style="padding: 11px 16px; background: linear-gradient(135deg, #4CAF50 0%, #45a049 100%); color: white; border: none; border-radius: 8px; cursor: pointer; font-size: 13px; font-weight: 700;">Save</button>
                    </div>
                </div>
            </div>
        `;

        document.body.appendChild(settingsPopup);
        document
            .getElementById("closeSettings")
            .addEventListener("click", hideSettingsPopup);
        document
            .getElementById("cancelSettings")
            .addEventListener("click", hideSettingsPopup);
        document
            .getElementById("saveSettings")
            .addEventListener("click", saveSettings);
        document
            .getElementById("clearConfig")
            .addEventListener("click", clearAllSettings);
        document
            .getElementById("killScript")
            .addEventListener("click", killScript);
        document
            .getElementById("copyRoomId")
            .addEventListener("click", () =>
                copySettingsText(
                    document.getElementById("roomIdInput").value.trim(),
                    "copyRoomId",
                ),
            );
        document
            .getElementById("roomIdInput")
            .addEventListener("keydown", (e) => {
                if (e.key === "Enter") saveSettings();
            });

        settingsEscHandler = (e) => {
            if (e.key === "Escape") hideSettingsPopup();
        };
        document.addEventListener("keydown", settingsEscHandler);

        // Keep keystrokes inside the dialog so Stremio's player shortcuts
        // (space, arrows, etc.) don't fire while typing in the settings.
        ["keydown", "keyup", "keypress"].forEach((evt) => {
            settingsPopup.addEventListener(evt, (e) => {
                e.stopPropagation();
                if (evt === "keydown" && e.key === "Escape")
                    hideSettingsPopup();
            });
        });

        document.getElementById("displayNameInput").focus();

        console.log("GUEST: Settings popup shown");
    }

    async function saveSettings() {
        const newRoomId = document.getElementById("roomIdInput").value.trim();
        const newDisplayName = document
            .getElementById("displayNameInput")
            .value.trim();

        if (!newRoomId) {
            alert("Room ID cannot be empty!");
            return;
        }

        const roomChanged = newRoomId !== ROOM_ID;
        const nameChanged = newDisplayName !== DISPLAY_NAME;

        if (!roomChanged && !nameChanged) {
            hideSettingsPopup();
            return;
        }

        if (isFollowingHost && roomChanged) {
            stopFollowingHost();
        }

        ROOM_ID = newRoomId;
        DISPLAY_NAME = newDisplayName;
        saveConfig();

        if (roomChanged) {
            const firebaseReady = await initializeFirebase();
            if (!firebaseReady) {
                alert(
                    "Room saved, but Firebase could not reconnect. Check the built Firebase config.",
                );
                return;
            }
        }

        hideSettingsPopup();
        showHostStatus(`Settings updated - Room: ${ROOM_ID}`);
        setTimeout(() => {
            const existingStatus = document.querySelector(
                ".host-status-message",
            );
            if (existingStatus) existingStatus.remove();
        }, 3000);
    }

    function updateSyncButtonState() {
        if (!watchTogetherButton) return;

        if (watchTogetherEnabled) {
            watchTogetherButton.title = "Sync On - following host";
            watchTogetherButton.style.background = "rgba(76, 175, 80, 0.72)";
            watchTogetherButton.style.borderColor = "rgba(170, 235, 175, 0.95)";
            watchTogetherButton.style.boxShadow =
                "inset 0 0 0 1px rgba(255, 255, 255, 0.16)";
            watchTogetherButton.style.opacity = "1";
        } else {
            watchTogetherButton.title = "Sync Off - click to follow host";
            watchTogetherButton.style.background = "rgba(10, 12, 16, 0.56)";
            watchTogetherButton.style.borderColor = "rgba(255, 255, 255, 0.22)";
            watchTogetherButton.style.boxShadow =
                "inset 0 -2px 0 rgba(76, 175, 80, 0.72)";
            watchTogetherButton.style.opacity = "0.92";
        }
    }

    // Toggle Watch Together functionality
    function toggleWatchTogether() {
        watchTogetherEnabled = !watchTogetherEnabled;

        if (watchTogetherEnabled) {
            updateSyncButtonState();
            console.log("GUEST: Watch Together ENABLED - Following host");
            startFollowingHost();
            updateRequestControlButton();
            showNotification("Synced - now following the host", "#4CAF50");
        } else {
            updateSyncButtonState();
            console.log("GUEST: Watch Together DISABLED");
            stopFollowingHost();
            removeGuestFromDatabase();
            updateRequestControlButton();
            showNotification(
                "Unsynced - you're watching on your own",
                "#ff9800",
            );
        }
    }

    // Get current time from timer
    function getCurrentTime() {
        if (videoElement && Number.isFinite(videoElement.currentTime)) {
            return videoElement.currentTime;
        }

        const timerElement = document.querySelector(".label-QFbsS");
        if (!timerElement) return 0;

        const timeStr = timerElement.textContent;
        const parts = timeStr.split(":").map(Number);
        if (parts.some((part) => Number.isNaN(part))) return 0;
        if (parts.length === 2) return parts[0] * 60 + parts[1];
        return parts[0] * 3600 + parts[1] * 60 + parts[2];
    }

    // Get play/pause state
    function getPlayState() {
        if (videoElement) {
            return !videoElement.paused;
        }

        if (!playPauseButton) return false;
        return playPauseButton.getAttribute("title") === "Pause";
    }

    function setPlayState(shouldPlay) {
        if (videoElement) {
            if (shouldPlay) {
                const playPromise = videoElement.play();
                if (playPromise && typeof playPromise.catch === "function") {
                    playPromise.catch(() => {
                        if (playPauseButton) playPauseButton.click();
                    });
                }
            } else {
                videoElement.pause();
            }
            return;
        }

        if (playPauseButton) {
            playPauseButton.click();
        }
    }

    // Check if guest video is buffering or not ready to resume.
    function isVideoBuffering({ includePaused = false } = {}) {
        if (videoElement) {
            if (videoElement.ended) return false;
            if (!includePaused && videoElement.paused) return false;

            // Transcoded Stremio streams can keep networkState at LOADING even
            // while playback is healthy, so rely on playable buffered data.
            return videoElement.readyState < HTMLMediaElement.HAVE_FUTURE_DATA;
        }

        const bufferingLayer = document.querySelector(".buffering-layer-ZZCYp");
        return Boolean(
            getPlayState() &&
                bufferingLayer &&
                bufferingLayer.style.display !== "none" &&
                bufferingLayer.offsetParent !== null,
        );
    }

    function getVideoDuration() {
        return videoElement && Number.isFinite(videoElement.duration)
            ? videoElement.duration
            : 0;
    }

    function getGuestPlaybackSnapshot() {
        const hasUsableVideo =
            videoElement &&
            Number.isFinite(videoElement.currentTime) &&
            (videoElement.readyState > 0 || getVideoDuration() > 0);

        if (!hasUsableVideo && lastReportedPlaybackState) {
            return {
                ...lastReportedPlaybackState,
                timeReliable: false,
                sampledAt: serverNow(),
                lastSeen: serverNow(),
                lastUpdated: serverNow(),
            };
        }

        const snapshot = {
            currentTime: getCurrentTime(),
            isPlaying: getPlayState(),
            isBuffering: isVideoBuffering({ includePaused: true }),
            duration: getVideoDuration(),
            timeReliable: !!hasUsableVideo,
            sampledAt: serverNow(),
            lastSeen: serverNow(),
            lastUpdated: serverNow(),
        };

        if (snapshot.timeReliable) {
            lastReportedPlaybackState = snapshot;
        }

        return snapshot;
    }

    function formatTimestamp(seconds) {
        const safeSeconds = Math.max(0, Math.floor(Number(seconds) || 0));
        const hours = Math.floor(safeSeconds / 3600);
        const minutes = Math.floor((safeSeconds % 3600) / 60);
        const secs = safeSeconds % 60;
        if (hours > 0) {
            return `${hours}:${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
        }
        return `${minutes}:${String(secs).padStart(2, "0")}`;
    }

    // Check if movie has loaded (timer shows actual time instead of --:--:--)
    function isMovieLoaded() {
        const timerElement = document.querySelector(".label-QFbsS");
        if (!timerElement) return false;

        const timeStr = timerElement.textContent;
        // Check if timer shows actual time (not --:--:--)
        return !timeStr.includes("--");
    }

    // Check if we're on the watch together redirect page
    function isOnWatchTogetherPage() {
        return (
            window.location.pathname.includes("/watchtogether") ||
            window.location.hash.includes("#/watchtogether")
        );
    }

    // Redirect to host's video URL
    function redirectToHostVideo(hostVideoURL) {
        if (hostVideoURL && hostVideoURL !== window.location.href) {
            console.log("GUEST: Redirecting to host video:", hostVideoURL);
            showHostStatus("Redirecting to host video...");
            setTimeout(() => {
                window.location.href = hostVideoURL;
            }, 2000);
        }
    }

    // Validate if URL is a valid Stremio video URL
    function isValidStremioVideoURL(url) {
        try {
            const urlObj = new URL(url);
            return (
                urlObj.hostname.includes("stremio.com") &&
                (urlObj.pathname.includes("/player/") ||
                    urlObj.hash.includes("#/player/"))
            );
        } catch (error) {
            return false;
        }
    }

    // Show watch together redirect page
    function showWatchTogetherRedirectPage() {
        // Clear the page content
        document.body.innerHTML = "";

        // Create redirect page with embedded HTML content
        const redirectPage = document.createElement("div");
        redirectPage.style.cssText = `
            margin: 0;
            padding: 0;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            font-family: Arial, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            color: white;
        `;

        redirectPage.innerHTML = `
            <div style="text-align: center; max-width: 600px; padding: 40px;">
                <h1 style="font-size: 3em; margin: 0 0 20px 0; text-shadow: 2px 2px 4px rgba(0,0,0,0.3);">
                    Watch Together
                </h1>
                <p style="font-size: 1.2em; margin: 0 0 30px 0; opacity: 0.9;">
                    Loading Stremio Watch Together...
                </p>
                <div style="margin: 30px 0;">
                    <div style="
                        width: 50px;
                        height: 50px;
                        border: 4px solid rgba(255,255,255,0.3);
                        border-top: 4px solid white;
                        border-radius: 50%;
                        animation: spin 1s linear infinite;
                        margin: 0 auto;
                    "></div>
                </div>
                <div style="background: rgba(255,255,255,0.1); padding: 20px; border-radius: 10px; margin: 20px 0;">
                    <div style="font-size: 1.2em; font-weight: bold; margin: 10px 0;">
                        Room ID: <strong>${ROOM_ID}</strong>
                    </div>
                    <p style="font-size: 1em; margin: 10px 0; opacity: 0.9;">
                        Make sure you have the Stremio Watch Together userscript installed!
                    </p>
                </div>
                <p style="font-size: 0.9em; margin: 10px 0; opacity: 0.7;">
                    Waiting for host to start the video...
                </p>
                <div style="margin-top: 30px;">
                    <button id="manualConnect" style="
                        background: rgba(255,255,255,0.2);
                        border: 2px solid white;
                        color: white;
                        padding: 12px 24px;
                        border-radius: 25px;
                        cursor: pointer;
                        font-size: 1em;
                        transition: all 0.3s ease;
                    " onmouseover="this.style.background='rgba(255,255,255,0.3)'"
                       onmouseout="this.style.background='rgba(255,255,255,0.2)'">
                        Manual Connect
                    </button>
                </div>
                <div id="userscriptStatus" style="margin-top: 20px; font-size: 0.9em; opacity: 0.8;">
                    Checking for userscript...
                </div>
            </div>
            <style>
                @keyframes spin {
                    0% { transform: rotate(0deg); }
                    100% { transform: rotate(360deg); }
                }
            </style>
        `;

        document.body.appendChild(redirectPage);

        // Add manual connect functionality
        document
            .getElementById("manualConnect")
            .addEventListener("click", () => {
                const newRoomId = prompt("Enter Room ID:", ROOM_ID);
                if (newRoomId && newRoomId !== ROOM_ID) {
                    ROOM_ID = newRoomId;
                    location.reload();
                }
            });

        // Check if userscript is loaded
        setTimeout(() => {
            const statusDiv = document.getElementById("userscriptStatus");
            if (typeof window.stremioWatchTogetherLoaded === "undefined") {
                statusDiv.innerHTML =
                    "WARNING: Stremio Watch Together userscript not detected!<br>Please install the userscript and refresh the page.";
                statusDiv.style.color = "#ffcc00";
            } else {
                statusDiv.innerHTML =
                    "SUCCESS: Userscript loaded successfully!";
                statusDiv.style.color = "#4CAF50";
            }
        }, 2000);

        console.log("GUEST: Watch Together redirect page displayed");
    }

    // Get controller state from Firebase data
    function getControllerState(data) {
        if (!data || !data.permissions || !data.permissions.controllerId) {
            return null;
        }

        const controllerId = data.permissions.controllerId;

        // Check if host has control
        if (data.host && data.host.userId === controllerId) {
            return data.host;
        }

        // Check if a guest has control
        if (data.guests && data.guests[controllerId]) {
            return data.guests[controllerId];
        }

        return null;
    }

    // Apply host's state to guest's video
    // Seek the video programmatically (force sync / seek-lock) so the resulting
    // "seeked" event isn't mistaken for a user scrub.
    function seekVideoTo(time) {
        if (!videoElement || !Number.isFinite(time)) return;
        lastProgrammaticSeekAt = Date.now();
        videoElement.currentTime = time;
    }

    // Estimate the host's current playback position. Extrapolates from the moment
    // the host sampled it (in shared server time), so both network latency and
    // device clock skew are corrected.
    function getExpectedHostTime() {
        if (!Number.isFinite(hostClockTime)) return null;
        const elapsed = hostClockPlaying
            ? Math.max(0, (serverNow() - hostClockSampledAt) / 1000)
            : 0;
        return hostClockTime + elapsed;
    }

    function applyHostState(hostState, options = {}) {
        if (!watchTogetherEnabled || !videoElement || !hostState) return;

        // If we have control, don't apply host state
        if (currentControllerId === USER_ID) {
            console.log("GUEST: Ignoring controller state - we have control");
            return;
        }

        console.log("GUEST: Applying controller state:", hostState);

        // Track the host's clock so seek-lock can extrapolate the live position.
        // sampledAt is in shared server time; fall back to receive time for old data.
        if (Number.isFinite(hostState.currentTime)) {
            hostClockTime = hostState.currentTime;
            hostClockSampledAt = Number.isFinite(hostState.sampledAt)
                ? hostState.sampledAt
                : serverNow();
            hostClockPlaying = !!hostState.isPlaying && !hostState.isBuffering;
        }

        const localIsPlaying = getPlayState();

        // Normal updates only report drift; Force Sync is the explicit seek action.
        if (options.force && hostState.currentTime !== undefined) {
            // Seek to the host's projected live position so transit time is included.
            const target = getExpectedHostTime() ?? hostState.currentTime;
            console.log(
                `GUEST: Force syncing time: local=${getCurrentTime()}s, host≈${target.toFixed(2)}s`,
            );
            seekVideoTo(target);
        }

        // Sync play/pause state
        if (hostState.isBuffering) {
            // Host is buffering - pause guest video
            if (localIsPlaying) {
                setPlayState(false);
            }
            showHostStatus("Controller is buffering...");
            showHostBufferingIcon();
        } else if (hostState.isPlaying && !localIsPlaying) {
            // Host is playing - resume guest video
            setPlayState(true);
            hideHostStatus();
            hideHostBufferingIcon();
        } else if (!hostState.isPlaying && localIsPlaying) {
            // Host is paused - pause guest video
            setPlayState(false);
            hideHostStatus();
            hideHostBufferingIcon();
        }

        lastKnownHostState = hostState;
    }

    // Show host status message
    function showHostStatus(message) {
        hideHostStatus(); // Remove existing message

        // Find control bar dynamically
        const currentControlBar = document.querySelector(
            ".control-bar-buttons-container-SWhkU",
        );
        if (!currentControlBar) return;

        const statusDiv = document.createElement("div");
        statusDiv.className = "host-status-message";
        statusDiv.style.cssText = `
            position: absolute;
            top: 10px;
            left: 50%;
            transform: translateX(-50%);
            background: rgba(0, 0, 0, 0.8);
            color: white;
            padding: 8px 16px;
            border-radius: 4px;
            font-size: 14px;
            z-index: 1000;
            pointer-events: none;
        `;
        statusDiv.textContent = message;

        const controlBarContainer = currentControlBar.closest(".control-bar");
        if (controlBarContainer) {
            controlBarContainer.style.position = "relative";
            controlBarContainer.appendChild(statusDiv);
        }
    }

    // Show host buffering loading icon
    function showHostBufferingIcon() {
        hideHostBufferingIcon(); // Remove existing icon

        const loadingIcon = document.createElement("div");
        loadingIcon.className = "host-buffering-icon";
        loadingIcon.style.cssText = `
            position: fixed;
            top: 20px;
            right: 20px;
            width: 36px;
            height: 36px;
            z-index: 10000;
            pointer-events: none;
            background: rgba(0, 0, 0, 0.8);
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
        `;

        loadingIcon.innerHTML = `
            <div style="
                width: 24px;
                height: 24px;
                border: 3px solid rgba(255, 107, 53, 0.3);
                border-top: 3px solid #FF6B35;
                border-radius: 50%;
                animation: spin 1s linear infinite;
            "></div>
            <style>
                @keyframes spin {
                    0% { transform: rotate(0deg); }
                    100% { transform: rotate(360deg); }
                }
            </style>
        `;

        // Add to body for better visibility
        document.body.appendChild(loadingIcon);
        console.log("GUEST: Host buffering icon displayed");
    }

    // Hide host buffering loading icon
    function hideHostBufferingIcon() {
        const existingIcon = document.querySelector(".host-buffering-icon");
        if (existingIcon) {
            existingIcon.remove();
        }
    }

    // Hide host status message
    function hideHostStatus() {
        const existingMessage = document.querySelector(".host-status-message");
        if (existingMessage) {
            existingMessage.remove();
        }
    }

    // Show notification message
    // Stack toasts in a shared container so multiple messages coexist instead of
    // clobbering each other. Uses textContent, so callers pass raw strings.
    function showNotification(message, color = "#4CAF50", duration = 3000) {
        injectPanelStyles();

        let container = document.querySelector(".wt-toast-container");
        if (!container) {
            container = document.createElement("div");
            container.className = "wt-toast-container";
            document.body.appendChild(container);
        }

        const notification = document.createElement("div");
        notification.className = "control-notification";
        notification.style.borderColor = color;
        notification.textContent = message;
        container.appendChild(notification);

        setTimeout(() => {
            notification.style.animation = "wtToastOut 0.25s ease forwards";
            setTimeout(() => {
                notification.remove();
                if (container && !container.childElementCount)
                    container.remove();
            }, 250);
        }, duration);
    }

    // Request control from host
    async function requestControl() {
        if (!roomRef) return;

        try {
            const { update } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");

            await update(roomRef, {
                [`permissions/controlRequests/${USER_ID}`]: {
                    userId: USER_ID,
                    displayName: DISPLAY_NAME,
                    requestedAt: Date.now(),
                },
            });

            pendingControlRequest = true;
            showNotification(
                "Control requested - waiting for host approval",
                "#ff9800",
            );
            console.log("GUEST: Control requested from host");

            // Update button state
            updateRequestControlButton();
        } catch (error) {
            console.error("GUEST ERROR: Failed to request control:", error);
            showNotification("Failed to request control", "#f44336");
        }
    }

    // Watch Firebase's connection state and toast on drops/recoveries.
    async function monitorConnection() {
        if (!database || monitoredDatabase === database) return;
        monitoredDatabase = database;
        wasConnected = null;
        hasLostConnection = false;

        try {
            const { ref, onValue } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");
            const connectedRef = ref(database, ".info/connected");
            if (connectionUnsubscribe) {
                connectionUnsubscribe();
            }

            connectionUnsubscribe = onValue(connectedRef, (snapshot) => {
                const connected = snapshot.val() === true;
                if (!connected && wasConnected === true) {
                    hasLostConnection = true;
                    showNotification(
                        "Connection lost - reconnecting…",
                        "#f44336",
                        6000,
                    );
                } else if (connected && hasLostConnection) {
                    hasLostConnection = false;
                    showNotification("Reconnected to the room", "#4CAF50");
                }
                wasConnected = connected;
            });
        } catch (error) {
            console.error("GUEST ERROR: Failed to monitor connection:", error);
        }
    }

    // Listen for host state changes
    async function startHostListener() {
        try {
            const { onValue } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");

            if (hostListenerUnsubscribe) {
                hostListenerUnsubscribe();
            }

            hostListenerUnsubscribe = onValue(roomRef, (snapshot) => {
                const data = snapshot.val();

                if (
                    data &&
                    data.forceSync &&
                    data.forceSync.syncId &&
                    data.forceSync.syncId !== lastAppliedForceSyncId
                ) {
                    lastAppliedForceSyncId = data.forceSync.syncId;
                    console.log("GUEST: Applying force sync:", data.forceSync);
                    applyHostState(data.forceSync, { force: true });
                }

                // Update permissions
                if (data && data.permissions) {
                    const previousControllerId = currentControllerId;
                    currentControllerId = data.permissions.controllerId;

                    // Notify if control changed
                    if (
                        previousControllerId !== currentControllerId &&
                        previousControllerId !== null
                    ) {
                        if (currentControllerId === USER_ID) {
                            showNotification(
                                "You now have control!",
                                "#4CAF50",
                            );
                        } else {
                            showNotification(
                                "Control returned to host",
                                "#FF6B35",
                            );
                        }
                    }

                    // Resolve a pending control request: if our request disappeared
                    // without us becoming the controller, the host denied it.
                    // (Approval is already announced by the control-change toast above.)
                    if (pendingControlRequest) {
                        const requests = data.permissions.controlRequests || {};
                        if (!requests[USER_ID]) {
                            pendingControlRequest = false;
                            if (currentControllerId !== USER_ID) {
                                showNotification(
                                    "Control request denied by host",
                                    "#f44336",
                                );
                            }
                        }
                    }

                    // Update UI
                    updateRequestControlButton();
                }

                if (data && data.host && data.status === "active") {
                    console.log("GUEST: Received room state update");

                    // Check if we need to redirect to host's video
                    if (
                        isWatchTogetherPage() &&
                        data.videoURL &&
                        isValidStremioVideoURL(data.videoURL)
                    ) {
                        redirectToHostVideo(data.videoURL);
                        return;
                    }

                    // Apply state from whoever has the control token
                    const controllerState = getControllerState(data);
                    if (controllerState) {
                        const isInitial = needsInitialSync;
                        if (isInitial) needsInitialSync = false;
                        console.log(
                            "GUEST: Applying state from controller:",
                            data.permissions.controllerId,
                        );
                        applyHostState(controllerState, { force: isInitial });
                    } else {
                        console.log("GUEST: No controller state found");
                    }
                } else if (data && data.status === "waiting_for_guests") {
                    console.log("GUEST: Waiting for host to start...");
                    showHostStatus("Waiting for host to start...");
                }
            });

            console.log("GUEST: Host listener started");
        } catch (error) {
            console.error("GUEST ERROR: Failed to start host listener:", error);
        }
    }

    // Send guest state to Firebase
    async function sendGuestState() {
        if (!watchTogetherEnabled || !roomRef) return;

        try {
            const { update } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");

            const playbackState = getGuestPlaybackSnapshot();
            const currentTime = playbackState.currentTime;
            const isPlaying = playbackState.isPlaying;
            const isCurrentlyBuffering = playbackState.isBuffering;

            // If we have control, send full state including video control
            // If not, only send status info
            const guestState = {
                userId: USER_ID,
                displayName: DISPLAY_NAME,
                currentTime: currentTime,
                isPlaying: isPlaying,
                isBuffering: isCurrentlyBuffering,
                duration: playbackState.duration,
                timeReliable: playbackState.timeReliable,
                sampledAt: playbackState.sampledAt,
                lastUpdated: playbackState.lastUpdated,
                lastSeen: playbackState.lastSeen,
                connected: true,
            };

            // Only send video control commands if we have the control token
            if (currentControllerId === USER_ID) {
                guestState.currentTime = currentTime;
                guestState.isPlaying = isPlaying;
                console.log("GUEST: Sending control state:", {
                    currentTime,
                    isPlaying,
                    isBuffering: isCurrentlyBuffering,
                });
            } else {
                console.log("GUEST: Sending status only (no control):", {
                    isBuffering: isCurrentlyBuffering,
                    timeReliable: playbackState.timeReliable,
                });
            }

            await update(roomRef, {
                ["guests/" + USER_ID]: guestState,
            });

            lastGuestStateSent = currentTime;
        } catch (error) {
            console.error("GUEST ERROR: Failed to send state:", error);
        }
    }

    // Send heartbeat to host
    async function sendHeartbeat() {
        if (!watchTogetherEnabled || !roomRef) return;

        try {
            const { update } =
                await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");

            await update(roomRef, {
                [`guests/${USER_ID}/userId`]: USER_ID,
                [`guests/${USER_ID}/displayName`]: DISPLAY_NAME,
                [`guests/${USER_ID}/connected`]: true,
                [`guests/${USER_ID}/lastSeen`]: serverNow(),
            });
        } catch (error) {
            console.error("GUEST ERROR: Failed to send heartbeat:", error);
        }
    }

    // Register this guest in the Firebase room
    async function registerGuestInRoom() {
        if (!roomRef) return;
        const { update, ref, onDisconnect } =
            await import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js");
        const playbackState = getGuestPlaybackSnapshot();
        await update(roomRef, {
            ["guests/" + USER_ID]: {
                userId: USER_ID,
                displayName: DISPLAY_NAME,
                currentTime: playbackState.currentTime,
                isPlaying: playbackState.isPlaying,
                isBuffering: playbackState.isBuffering,
                duration: playbackState.duration,
                timeReliable: playbackState.timeReliable,
                connected: true,
                sampledAt: playbackState.sampledAt,
                lastSeen: playbackState.lastSeen,
                lastUpdated: playbackState.lastUpdated,
            },
        });

        // Auto-remove this guest from the database if the connection drops
        // (tab closed, network lost) without an explicit unsync.
        try {
            const guestRef = ref(
                database,
                `rooms/${ROOM_ID}/guests/${USER_ID}`,
            );
            await onDisconnect(guestRef).remove();
        } catch (error) {
            console.error(
                "GUEST ERROR: Failed to set up disconnect cleanup:",
                error,
            );
        }

        console.log("GUEST: Registered in room");
    }

    // Start following host
    function startFollowingHost() {
        isFollowingHost = true;
        needsInitialSync = true;

        // Register in the room now that the guest has chosen to sync
        registerGuestInRoom();

        // Send state updates every 2 seconds
        if (stateInterval) clearInterval(stateInterval);
        stateInterval = setInterval(() => {
            if (isFollowingHost) {
                sendGuestState();
            } else {
                clearInterval(stateInterval);
            }
        }, 2000);

        // Send heartbeat every 10 seconds
        if (heartbeatInterval) clearInterval(heartbeatInterval);
        heartbeatInterval = setInterval(() => {
            if (isFollowingHost) {
                sendHeartbeat();
            } else {
                clearInterval(heartbeatInterval);
            }
        }, 10000);

        // Set up buffering observer
        setupBufferingObserver();

        console.log("GUEST: Started following host");
    }

    // Set up buffering observer
    function setupBufferingObserver() {
        if (videoElement && videoStateListeners.length === 0) {
            videoStateListeners = [
                "play",
                "pause",
                "waiting",
                "playing",
                "canplay",
            ].map((eventName) => {
                const listener = () => {
                    if (watchTogetherEnabled) {
                        sendGuestState();
                    }
                };
                videoElement.addEventListener(eventName, listener);
                return { eventName, listener };
            });

            // Seek-lock: when a synced guest who isn't the controller scrubs away,
            // snap them back to the host's live position.
            const seekedListener = () => {
                if (!watchTogetherEnabled) return;

                const wasProgrammatic =
                    Date.now() - lastProgrammaticSeekAt < 800;
                if (
                    !wasProgrammatic &&
                    isFollowingHost &&
                    currentControllerId !== USER_ID
                ) {
                    const expected = getExpectedHostTime();
                    if (
                        expected !== null &&
                        Math.abs(getCurrentTime() - expected) >
                            SEEK_LOCK_THRESHOLD
                    ) {
                        console.log(
                            `GUEST: Seek blocked - snapping back to host (${expected.toFixed(1)}s)`,
                        );
                        seekVideoTo(expected);
                        showNotification(
                            "Seeking is locked while synced - snapped back to host",
                            "#ff9800",
                        );
                        return;
                    }
                }

                sendGuestState();
            };
            videoElement.addEventListener("seeked", seekedListener);
            videoStateListeners.push({
                eventName: "seeked",
                listener: seekedListener,
            });
        }

        const bufferingLayer = document.querySelector(".buffering-layer-ZZCYp");
        if (bufferingLayer) {
            bufferingObserver = new MutationObserver(() => {
                const currentlyBuffering = isVideoBuffering({
                    includePaused: true,
                });
                if (currentlyBuffering !== isGuestBuffering) {
                    isGuestBuffering = currentlyBuffering;
                    if (watchTogetherEnabled) {
                        sendGuestState();
                        if (currentlyBuffering) {
                            console.log(
                                "GUEST: Buffering detected - notifying host",
                            );
                        } else {
                            console.log("GUEST: Buffering ended");
                        }
                    }
                }
            });

            bufferingObserver.observe(bufferingLayer, {
                attributes: true,
                childList: true,
                subtree: true,
            });
        }
    }

    // Stop following host
    function stopFollowingHost() {
        isFollowingHost = false;
        hostClockTime = null;
        pendingControlRequest = false;
        if (stateInterval) {
            clearInterval(stateInterval);
            stateInterval = null;
        }
        if (heartbeatInterval) {
            clearInterval(heartbeatInterval);
            heartbeatInterval = null;
        }
        hideHostStatus();
        hideHostBufferingIcon();

        if (bufferingObserver) {
            bufferingObserver.disconnect();
            bufferingObserver = null;
        }
        if (videoElement) {
            for (const { eventName, listener } of videoStateListeners) {
                videoElement.removeEventListener(eventName, listener);
            }
        }
        videoStateListeners = [];

        console.log("GUEST: Stopped following host");
    }

    // Remove this guest's entry from the Firebase database
    function removeGuestFromDatabase() {
        if (!roomRef || !database) return;

        const guestRoomRef = roomRef;
        const guestDatabase = database;
        const guestPath = `rooms/${ROOM_ID}/guests/${USER_ID}`;

        import("https://www.gstatic.com/firebasejs/12.4.0/firebase-database.js").then(
            ({ update, ref, onDisconnect }) => {
                // Cancel the armed disconnect handler since we're leaving cleanly.
                try {
                    onDisconnect(ref(guestDatabase, guestPath)).cancel();
                } catch (error) {
                    // Ignore - the disconnect handler may not be set
                }
                update(guestRoomRef, {
                    ["guests/" + USER_ID]: null,
                }).catch(() => {
                    // Ignore removal errors
                });
            },
        );
    }

    // Cleanup function
    function cleanup() {
        if (watchTogetherButton) watchTogetherButton.remove();
        if (requestControlButton) requestControlButton.remove();
        if (settingsButton) settingsButton.remove();
        watchTogetherButton = null;
        requestControlButton = null;
        settingsButton = null;
        hideSettingsPopup();
        hideHostStatus();
        hideHostBufferingIcon();
        if (bufferingObserver) {
            bufferingObserver.disconnect();
            bufferingObserver = null;
        }
        if (videoElement) {
            for (const { eventName, listener } of videoStateListeners) {
                videoElement.removeEventListener(eventName, listener);
            }
        }
        videoStateListeners = [];

        removeGuestFromDatabase();
        cleanupRealtimeListeners();

        videoElement = null;
        playPauseButton = null;
        controlBar = null;
        watchTogetherEnabled = false;
        isScriptActive = false;
        console.log("GUEST: Cleanup complete");
    }

    // Get room ID from URL parameters
    function getRoomIdFromURL() {
        const urlParams = new URLSearchParams(window.location.search);
        const roomFromURL = urlParams.get("room");
        if (roomFromURL) {
            console.log("GUEST: Room ID from URL:", roomFromURL);
            return roomFromURL;
        }
        return null;
    }

    // Main initialization
    async function initialize() {
        // Load saved configuration first
        loadConfig();

        // Initialize display name (generate if needed, or load saved)
        initializeDisplayName();

        // Check if room ID is provided in URL
        const roomFromURL = getRoomIdFromURL();
        if (roomFromURL) {
            ROOM_ID = roomFromURL;
            console.log("GUEST: Using room ID from URL:", ROOM_ID);
        }

        console.log(`GUEST User ID: ${USER_ID}`);
        console.log(`Room ID: ${ROOM_ID}`);

        const firebaseReady = await initializeFirebase();
        if (!firebaseReady) {
            console.log(
                "GUEST WARNING: Firebase not configured - user needs to set up Firebase first",
            );
            // Don't return here, let the user configure Firebase through the settings
            // The buttons will be created but Firebase won't be initialized until configured
        }

        // If on watch together redirect page, show redirect page and start listening for host
        if (isWatchTogetherPage()) {
            console.log("GUEST: On redirect page - showing redirect interface");
            showWatchTogetherRedirectPage();
            startHostListener();
            return;
        }

        // Wait for DOM elements with longer timeout
        const maxAttempts = 60; // Increased from 30 to 60
        let attempts = 0;

        console.log("GUEST: Waiting for DOM elements to load...");

        while (!findDOMElements() && attempts < maxAttempts) {
            await new Promise((resolve) => setTimeout(resolve, 1000));
            attempts++;

            if (attempts % 10 === 0) {
                console.log(
                    `GUEST: Still waiting for DOM elements... (${attempts}/${maxAttempts})`,
                );
            }
        }

        // Wait for movie to load (timer shows actual time instead of --:--:--)
        console.log("GUEST: Waiting for movie to load...");
        let movieLoadAttempts = 0;
        const maxMovieLoadAttempts = 30; // 30 seconds to wait for movie load

        while (!isMovieLoaded() && movieLoadAttempts < maxMovieLoadAttempts) {
            await new Promise((resolve) => setTimeout(resolve, 1000));
            movieLoadAttempts++;

            if (movieLoadAttempts % 5 === 0) {
                console.log(
                    `GUEST: Waiting for movie to load... (${movieLoadAttempts}/${maxMovieLoadAttempts})`,
                );
            }
        }

        if (movieLoadAttempts >= maxMovieLoadAttempts) {
            console.log(
                "GUEST WARNING: Movie load timeout - proceeding anyway",
            );
        } else {
            console.log("GUEST: Movie loaded successfully");
        }

        if (attempts >= maxAttempts) {
            console.error(
                "GUEST ERROR: Could not find required DOM elements after 60 seconds",
            );
            console.error("GUEST ERROR: This might be because:");
            console.error("   1. The video is still loading");
            console.error("   2. Stremio UI has changed");
            console.error("   3. The page structure is different");

            // Try to continue anyway with partial elements
            if (videoElement || controlBar || playPauseButton) {
                console.log(
                    "GUEST WARNING: Attempting to continue with partial elements...",
                );
            } else {
                return;
            }
        }

        injectPanelStyles();
        createWatchTogetherButton();
        createRequestControlButton();
        createSettingsButton();
        startHostListener();

        console.log("GUEST: Watch Together Script loaded successfully!");
        console.log("Click the green chat icon to start following the host");
        console.log(
            "Make sure you're using the same Room ID as the host:",
            ROOM_ID,
        );
    }

    // Handle page unload
    beforeUnloadHandler = cleanup;
    window.addEventListener("beforeunload", beforeUnloadHandler);

    // Wait for page to be fully loaded before initializing
    function waitForPageLoad() {
        return new Promise((resolve) => {
            if (document.readyState === "complete") {
                resolve();
            } else {
                window.addEventListener("load", resolve);
            }
        });
    }

    // Start initialization
    async function startInitialization() {
        console.log("GUEST: Starting initialization...");

        // Wait for page load
        await waitForPageLoad();
        console.log("GUEST: Page loaded");

        // Wait a bit more for Stremio to initialize
        await new Promise((resolve) => setTimeout(resolve, 3000));
        console.log("GUEST: Waiting period complete");

        // Start the main initialization
        await initialize();
    }

    // Start initialization
    // Start initialization
    // startInitialization().catch(error => {
    //     console.error('GUEST ERROR: Initialization failed:', error);
    // });

    // URL Change Detection and Lifecycle Management
    let lastUrl = window.location.href;

    async function checkUrlAndManageState() {
        if (scriptKilled) return;

        const currentUrl = window.location.href;
        const onPlayer = isPlayerPage();
        const onWatchTogether = isWatchTogetherPage();

        if (onPlayer || onWatchTogether) {
            if (!isScriptActive && !isInitializationRunning) {
                console.log(
                    "GUEST: Relevant page detected, initializing Watch Together...",
                );
                if (onWatchTogether) {
                    console.log("GUEST: On watch together redirect page");
                }

                isInitializationRunning = true;

                try {
                    // Start initialization flow
                    await startInitialization(); // This calls initialize() which sets up everything
                    isScriptActive = true;
                } catch (error) {
                    console.error("GUEST ERROR: Failed to initialize:", error);
                    // Reset flags so we can retry if needed
                    isScriptActive = false;
                } finally {
                    isInitializationRunning = false;
                }
            }
        } else {
            if (isScriptActive) {
                console.log("GUEST: Left relevant page, cleaning up...");
                cleanup();
                // isScriptActive is set to false in cleanup(), but ensuring it here too
                isScriptActive = false;
            }
        }

        lastUrl = currentUrl;
    }

    // Interval to check for URL changes (robust for SPA)
    // Check every 1 second
    urlMonitorInterval = setInterval(checkUrlAndManageState, 1000);

    // Initial check
    checkUrlAndManageState();

    // Also listen for popstate just in case
    popstateHandler = () => {
        setTimeout(checkUrlAndManageState, 100);
    };
    window.addEventListener("popstate", popstateHandler);
})();
