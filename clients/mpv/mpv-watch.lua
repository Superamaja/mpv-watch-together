local mp = require "mp"
local msg = require "mp.msg"
local options = require "mp.options"
local utils = require "mp.utils"

local input_ok, input = pcall(require, "mp.input")

local opts = {
    helper_url = "http://127.0.0.1:8765",
    ipc_server = "",
    role = "guest",
    room = "",
    display_name = "mpv watcher",
    sync_on_start = "no",
    heartbeat_interval = 5.0,
    command_interval = 0.5,
    seek_lock = "yes",
    seek_lock_threshold = 3.0,
    auto_force_sync_on_seek = "yes",
    host_seek_threshold = 2.5,
    host_seek_cooldown = 1.5,
}

options.read_options(opts, "mpv-watch")

local sync_enabled = opts.sync_on_start == "yes"
local last_force_sync_id = nil
local last_track_sync_id = nil
local last_event_id = nil
local last_server_now = nil
local last_server_wall = nil
local applying_remote_seek = false
local applying_remote_pause = false
local force_next_host_apply = false
local last_host_state = nil
local last_time_pos = nil
local last_time_wall = nil
local last_auto_force_sync_at = 0
local host_found_notified = false
local helper_connected = nil  -- nil=never tried, true=ok, false=was ok then lost
local runtime_ready = false
local ipc_server_started = false
local observers_registered = false
local heartbeat_timer = nil
local command_timer = nil
local pending_state_timer = nil
local poll_commands = nil
local set_sync = nil
local send_state = nil
local ensure_runtime_ready = nil

local path_separator = package.config:sub(1, 1)
local is_windows = path_separator == "\\"
math.randomseed(os.time() + math.floor(mp.get_time() * 1000000))

local function heartbeat_interval()
    return tonumber(opts.heartbeat_interval) or 5.0
end

local function start_timers()
    helper_connected = nil
    if not heartbeat_timer then
        heartbeat_timer = mp.add_periodic_timer(heartbeat_interval(), function() send_state() end)
    end
    if not command_timer then
        command_timer = mp.add_periodic_timer(opts.command_interval, function() poll_commands() end)
    end
end

local function stop_timers()
    if heartbeat_timer then
        heartbeat_timer:kill()
        heartbeat_timer = nil
    end
    if command_timer then
        command_timer:kill()
        command_timer = nil
    end
    if pending_state_timer then
        pending_state_timer:kill()
        pending_state_timer = nil
    end
end

local function should_show_event(event, user_id)
    if not event or not event.message then
        return false
    end
    if event.type == "force_sync" or event.type == "auto_force_sync" then
        return opts.role == "host"
    end
    if event.type == "config_changed" then
        return true
    end
    return event.userId ~= nil and event.userId ~= "" and event.userId ~= user_id
end

local function trim(value)
    if type(value) ~= "string" then
        return ""
    end
    return value:gsub("^%s+", ""):gsub("%s+$", "")
end

local function ipc_server_name()
    local pid = trim(mp.get_property("pid", ""))
    if pid == "" then
        pid = tostring(math.floor(mp.get_time() * 1000000))
    end
    return "mpv-watch-" .. pid .. "-" .. tostring(math.random(100000, 999999))
end

local function ipc_path_exists(path)
    if is_windows then
        return false
    end
    return utils.file_info(path) ~= nil
end

local function default_ipc_server()
    local name = ipc_server_name()
    if is_windows then
        return "\\\\.\\pipe\\" .. name
    end
    local tmp_dir = os.getenv("TMPDIR") or "/tmp"
    return utils.join_path(tmp_dir, name .. ".sock")
end

local function unique_ipc_server_path(path)
    if is_windows or not ipc_path_exists(path) then
        return path
    end

    local attempt = 0
    while ipc_path_exists(path) and attempt < 20 do
        attempt = attempt + 1
        path = path .. "-" .. tostring(attempt)
    end
    return path
end

local function ensure_ipc_server()
    if ipc_server_started then
        return true
    end

    local ipc_server = trim(opts.ipc_server)
    if ipc_server == "" then
        ipc_server = default_ipc_server()
    end
    ipc_server = unique_ipc_server_path(ipc_server)

    local ok, err = pcall(mp.set_property, "input-ipc-server", ipc_server)
    if not ok then
        msg.error("Failed to open mpv IPC server: " .. tostring(err))
        mp.osd_message("Watch Together: failed to open mpv IPC")
        return false
    end

    opts.ipc_server = ipc_server
    ipc_server_started = true
    msg.info("Opened mpv IPC server: " .. opts.ipc_server)
    return true
end

local function request(method, path, body)
    local args = {
        "curl",
        "-sS",
        "-X",
        method,
        "-H",
        "Content-Type: application/json",
        "-w",
        "\n%{http_code}",
    }
    if body ~= nil then
        args[#args + 1] = "--data"
        args[#args + 1] = utils.format_json(body)
    end
    args[#args + 1] = opts.helper_url .. path

    local result = utils.subprocess({
        args = args,
        cancellable = false,
        max_size = 1024 * 1024,
    })
    if result.status ~= 0 then
        return nil, result.error or result.stderr or "request failed"
    end
    local body, status_text = result.stdout:match("^(.*)\n(%d%d%d)%s*$")
    local status = tonumber(status_text or "")
    if not status then
        body = result.stdout
        status = 200
    end

    if body == "" then
        if status >= 400 then
            return nil, "request failed with HTTP " .. status
        end
        return {}, nil
    end

    local parsed = utils.parse_json(body)
    if status >= 400 then
        local message = "request failed with HTTP " .. status
        if type(parsed) == "table" and parsed.error then
            message = parsed.error
        end
        return nil, message
    end
    return parsed, nil
end

local function post_config()
    local result, err = request("POST", "/api/config", {
        role = opts.role,
        roomId = opts.room,
        displayName = opts.display_name,
        mpvIpcServer = opts.ipc_server,
    })
    if err then
        msg.warn("Failed to save helper config: " .. err)
        return false
    end
    if result and result.eventId then
        last_event_id = result.eventId
    end
    msg.debug("Saved helper config: room=" .. opts.room .. " displayName=" .. opts.display_name)
    return true
end

set_sync = function(enabled)
    if enabled and not ensure_runtime_ready() then
        sync_enabled = false
        stop_timers()
        return
    end

    local _, err = request("POST", "/api/sync", { enabled = enabled })
    if err then
        sync_enabled = false
        stop_timers()
        if enabled and err == "no host found in room" then
            mp.osd_message("Watch Together: sync disabled: no host found in room")
        else
            mp.osd_message("Watch Together: helper unavailable")
        end
        msg.warn("Failed to set sync: " .. err)
        return
    end
    sync_enabled = enabled
    if enabled then
        start_timers()
        send_state()
    else
        stop_timers()
    end
    mp.osd_message(enabled and "Watch Together: sync on" or "Watch Together: sync off")
    if enabled and opts.role == "guest" then
        force_next_host_apply = true
        host_found_notified = false
        poll_commands()
    elseif not enabled then
        host_found_notified = false
    end
end

local function save_runtime_config(changed_room)
    if not ensure_runtime_ready() then
        return false
    end

    local was_synced = sync_enabled
    if changed_room and was_synced then
        set_sync(false)
    end

    local saved = post_config()

    if changed_room then
        last_host_state = nil
        host_found_notified = false
        force_next_host_apply = opts.role == "guest"
        if was_synced then
            set_sync(true)
        end
    elseif saved and sync_enabled and send_state then
        send_state()
    end

    return saved
end

local function is_buffering()
    if mp.get_property_bool("paused-for-cache", false) then
        return true
    end

    local buffering_state = mp.get_property_number("cache-buffering-state", -1)
    if buffering_state >= 0 and buffering_state < 100 then
        return true
    end

    local cache_state = mp.get_property_native("demuxer-cache-state")
    return type(cache_state) == "table" and cache_state.underrun == true
end

local function track_id(property)
    return trim(mp.get_property(property, "no"))
end

local function playback_state()
    return {
        currentTime = mp.get_property_number("time-pos", 0),
        isPlaying = not mp.get_property_bool("pause", true),
        isBuffering = is_buffering(),
        duration = mp.get_property_number("duration", 0),
        aid = track_id("aid"),
        sid = track_id("sid"),
    }
end

local function current_server_millis()
    if type(last_server_now) == "number" and type(last_server_wall) == "number" then
        return last_server_now + math.max(0, mp.get_time() - last_server_wall) * 1000
    end
    return os.time() * 1000
end

send_state = function()
    if pending_state_timer then
        pending_state_timer:kill()
        pending_state_timer = nil
    end
    if not sync_enabled then
        return
    end
    local _, err = request("POST", "/api/mpv/state", playback_state())
    if err then
        msg.warn("Failed to send playback state: " .. err)
    end
end

local function queue_state_update()
    if not sync_enabled or pending_state_timer then
        return
    end
    pending_state_timer = mp.add_timeout(0.05, function()
        pending_state_timer = nil
        send_state()
    end)
end

local function projected_time(state)
    if not state or type(state.currentTime) ~= "number" then
        return nil
    end
    if not state.isPlaying or state.isBuffering or type(state.sampledAt) ~= "number" then
        return state.currentTime
    end
    local elapsed = math.max(0, (current_server_millis() - state.sampledAt) / 1000)
    return state.currentTime + elapsed
end

local function apply_remote_state(state, force)
    if not sync_enabled or opts.role == "host" or not state then
        return
    end

    if type(state.currentTime) == "number" then
        last_host_state = state
    end

    if force then
        local target = projected_time(state)
        if target then
            applying_remote_seek = true
            mp.set_property_number("time-pos", target)
            applying_remote_seek = false
        end
    end

    applying_remote_pause = true
    if state.isBuffering then
        mp.set_property_bool("pause", true)
        mp.osd_message("Watch Together: host is buffering")
    else
        mp.set_property_bool("pause", not state.isPlaying)
    end
    applying_remote_pause = false
end

local function display_track_id(value)
    value = trim(tostring(value or ""))
    if value == "" then
        return "default"
    end
    if value == "no" then
        return "off"
    end
    return value
end

local function apply_track_sync(track_sync)
    if not sync_enabled or opts.role == "host" or not track_sync then
        return
    end

    if track_sync.aid ~= nil and tostring(track_sync.aid) ~= "" then
        mp.set_property("aid", tostring(track_sync.aid))
    end
    if track_sync.sid ~= nil and tostring(track_sync.sid) ~= "" then
        mp.set_property("sid", tostring(track_sync.sid))
    end
    mp.osd_message("Watch Together: received tracks - audio " .. display_track_id(track_sync.aid) .. ", subtitles " .. display_track_id(track_sync.sid))
end

poll_commands = function()
    local data, err = request("GET", "/api/mpv/commands")
    if err or not data then
        if helper_connected == true then
            helper_connected = false
            mp.osd_message("Watch Together: connection lost, reconnecting")
        else
            helper_connected = false
        end
        return
    end
    if helper_connected == false then
        mp.osd_message("Watch Together: reconnected")
    end
    helper_connected = true
    if type(data.serverNow) == "number" then
        last_server_now = data.serverNow
        last_server_wall = mp.get_time()
    end
    if data.latestEvent and data.latestEvent.eventId and data.latestEvent.eventId ~= last_event_id then
        last_event_id = data.latestEvent.eventId
        if should_show_event(data.latestEvent, data.userId) then
            mp.osd_message("Watch Together: " .. data.latestEvent.message)
        end
        if opts.role == "host" and data.latestEvent.type == "guest_buffering" then
            mp.set_property_bool("pause", true)
        end
    end
    if data.syncEnabled ~= nil then
        local was_sync_enabled = sync_enabled
        sync_enabled = data.syncEnabled
        if was_sync_enabled and not sync_enabled and opts.role == "guest" then
            host_found_notified = false
            mp.osd_message("Watch Together: sync disabled: no host found in room")
        end
    end
    if data.forceSync and data.forceSync.syncId and data.forceSync.syncId ~= last_force_sync_id then
        last_force_sync_id = data.forceSync.syncId
        apply_remote_state(data.forceSync, true)
        if data.forceSync.reason == "auto_seek" then
            mp.osd_message("Watch Together: synced to host seek")
        else
            mp.osd_message("Watch Together: force synced")
        end
        return
    end
    if data.trackSync and data.trackSync.syncId and data.trackSync.syncId ~= last_track_sync_id then
        last_track_sync_id = data.trackSync.syncId
        apply_track_sync(data.trackSync)
    end
    if data.host then
        local force = force_next_host_apply
        force_next_host_apply = false
        apply_remote_state(data.host, force)
        if opts.role == "guest" and sync_enabled and not host_found_notified then
            host_found_notified = true
            mp.osd_message(force and "Watch Together: host found, synced" or "Watch Together: host found")
        elseif force then
            mp.osd_message("Watch Together: synced to host")
        end
    elseif opts.role == "guest" and sync_enabled then
        host_found_notified = false
    end
end

local function force_sync(current_time, reason)
    if not ensure_runtime_ready() then
        return
    end

    local state = playback_state()
    if type(current_time) == "number" then
        state.currentTime = current_time
    end
    if type(reason) == "string" and reason ~= "" then
        state.reason = reason
    end

    local result, err = request("POST", "/api/host/force-sync", state)
    if err then
        mp.osd_message("Watch Together: force sync failed")
        msg.warn("Force sync failed: " .. err)
        return
    end
    if result and result.message then
        if result.eventId then
            last_event_id = result.eventId
        end
        mp.osd_message("Watch Together: " .. result.message)
    else
        mp.osd_message("Watch Together: force sync sent")
    end
end

local function prompt_text(prompt, default, callback)
    if input_ok and input.get then
        input.get({
            prompt = prompt,
            default_text = default or "",
            cursor_position = #(default or "") + 1,
            submit = callback,
        })
        return
    end
    mp.osd_message(prompt .. " requires mp.input support")
end

local function prompt_text_next_tick(prompt, default, callback)
    mp.add_timeout(0.05, function()
        prompt_text(prompt, default, callback)
    end)
end

local function show_menu()
    if not ensure_runtime_ready() then
        return
    end

    local actions = {
        { label = sync_enabled and "Turn sync off" or "Turn sync on", id = "toggle" },
        { label = "Set room", id = "room" },
        { label = "Set display name", id = "name" },
    }

    if input_ok and input.select then
        local labels = {}
        for index, action in ipairs(actions) do
            labels[index] = action.label
        end

        local ok, err = pcall(input.select, {
            prompt = "Watch Together",
            items = labels,
            submit = function(index)
                local action = actions[index]
                local id = action and action.id
                if id == "toggle" then
                    set_sync(not sync_enabled)
                elseif id == "room" then
                    prompt_text_next_tick("Room", opts.room, function(value)
                        local next_room = trim(value)
                        if next_room ~= "" and next_room ~= opts.room then
                            opts.room = next_room
                            if save_runtime_config(true) then
                                mp.osd_message("Watch Together: room set to " .. opts.room)
                            else
                                mp.osd_message("Watch Together: room update failed")
                            end
                        elseif next_room == opts.room then
                            mp.osd_message("Watch Together: room unchanged")
                        end
                    end)
                elseif id == "name" then
                    prompt_text_next_tick("Display name", opts.display_name, function(value)
                        local next_name = trim(value)
                        if next_name ~= "" and next_name ~= opts.display_name then
                            opts.display_name = next_name
                            if save_runtime_config(false) then
                                mp.osd_message("Watch Together: name set to " .. opts.display_name)
                            else
                                mp.osd_message("Watch Together: name update failed")
                            end
                        elseif next_name == opts.display_name then
                            mp.osd_message("Watch Together: name unchanged")
                        end
                    end)
                end
            end,
        })
        if ok then
            return
        end

        msg.warn("Watch Together menu failed: " .. tostring(err))
    else
        msg.warn("mp.input.select is unavailable; falling back to sync toggle")
    end

    mp.osd_message("Watch Together: Ctrl+w toggles sync")
    set_sync(not sync_enabled)
end

local function register_observers()
    if observers_registered then
        return
    end
    observers_registered = true

    mp.observe_property("pause", "bool", function(_, paused)
        queue_state_update()
        if paused or not sync_enabled or opts.role ~= "guest" or applying_remote_pause then
            return
        end
        -- Guest manually unpaused while host is paused - re-pause and snap back.
        if not last_host_state or last_host_state.isPlaying then
            return
        end
        local target = projected_time(last_host_state)
        applying_remote_pause = true
        mp.set_property_bool("pause", true)
        applying_remote_pause = false
        if target then
            applying_remote_seek = true
            mp.set_property_number("time-pos", target)
            applying_remote_seek = false
        end
        mp.osd_message("Watch Together: paused by host")
    end)

    mp.observe_property("time-pos", "number", function(_, current_time)
        if not current_time then
            return
        end

        local now = mp.get_time()
        if applying_remote_seek then
            last_time_pos = current_time
            last_time_wall = now
            return
        end

        if last_time_pos and last_time_wall then
            local elapsed = math.max(0, now - last_time_wall)
            local paused = mp.get_property_bool("pause", true)
            local expected_time = last_time_pos + (paused and 0 or elapsed)
            if math.abs(current_time - expected_time) >= 1.0 then
                queue_state_update()
            end
        end

        if opts.role == "host" and sync_enabled and opts.auto_force_sync_on_seek == "yes" and last_time_pos and last_time_wall then
            local elapsed = math.max(0, now - last_time_wall)
            local expected_time = last_time_pos + elapsed
            local drift = math.abs(current_time - expected_time)
            local threshold = tonumber(opts.host_seek_threshold) or 2.5
            local cooldown = tonumber(opts.host_seek_cooldown) or 1.5
            if drift >= threshold and now - last_auto_force_sync_at >= cooldown then
                last_auto_force_sync_at = now
                force_sync(current_time, "auto_seek")
            end
        elseif opts.role == "guest" and sync_enabled and opts.seek_lock == "yes" and last_host_state then
            local expected_host_time = projected_time(last_host_state)
            local threshold = tonumber(opts.seek_lock_threshold) or 3.0
            if expected_host_time and math.abs(current_time - expected_host_time) >= threshold then
                applying_remote_seek = true
                mp.set_property_number("time-pos", expected_host_time)
                applying_remote_seek = false
                mp.osd_message("Watch Together: snapped back to host")
            end
        end

        last_time_pos = current_time
        last_time_wall = now
    end)

    for _, property in ipairs({
        "aid",
        "sid",
        "duration",
        "paused-for-cache",
        "cache-buffering-state",
        "demuxer-cache-state",
    }) do
        mp.observe_property(property, "native", function()
            queue_state_update()
        end)
    end

    mp.register_event("file-loaded", function()
        queue_state_update()
    end)
end

ensure_runtime_ready = function()
    if runtime_ready then
        return true
    end
    if not ensure_ipc_server() then
        return false
    end
    register_observers()
    runtime_ready = true
    return true
end

-- Lazily push config on the first menu open so startup stays near zero-cost when
-- sync_on_start=no. Once config is pushed (or sync is already on) this is a
-- no-op on every subsequent call.
local config_pushed = false

mp.add_key_binding("Ctrl+w", "mpv-watch-menu", function()
    if not ensure_runtime_ready() then
        return
    end
    if not config_pushed and not sync_enabled then
        config_pushed = true
        post_config()
    end
    show_menu()
end)

if opts.sync_on_start == "yes" then
    if ensure_runtime_ready() then
        config_pushed = true
        post_config()
        set_sync(true)
    end
end
