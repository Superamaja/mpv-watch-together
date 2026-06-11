local mp = require "mp"
local msg = require "mp.msg"
local options = require "mp.options"
local utils = require "mp.utils"

local input_ok, input = pcall(require, "mp.input")

local OSD_PREFIX = "Watch Together: "

local ROOM_SETTING_DEFAULTS = {
    command_interval = 0.5,
    idle_command_interval = 1.25,
    active_command_interval = 0.35,
    reconnect_backoff_max = 8.0,
    seek_lock_threshold = 3.0,
    host_seek_threshold = 2.5,
    host_seek_cooldown = 1.5,
}

local ROOM_SETTING_LIMITS = {
    command_interval = { min = 0.25, max = 3.0 },
    idle_command_interval = { min = 0.5, max = 5.0 },
    active_command_interval = { min = 0.25, max = 2.0 },
    reconnect_backoff_max = { min = 1.0, max = 15.0 },
    seek_lock_threshold = { min = 0.5, max = 10.0 },
    host_seek_threshold = { min = 0.5, max = 10.0 },
    host_seek_cooldown = { min = 0.25, max = 10.0 },
}

local opts = {
    helper_url = "http://127.0.0.1:8765",
    room = "",
    display_name = "mpv watcher",
    sync_on_start = "no",
    heartbeat_interval = 5.0,
    command_interval = ROOM_SETTING_DEFAULTS.command_interval,
    max_reconnect_polls = 12,
    adaptive_polling = "no",
    idle_command_interval = ROOM_SETTING_DEFAULTS.idle_command_interval,
    active_command_interval = ROOM_SETTING_DEFAULTS.active_command_interval,
    reconnect_backoff_max = ROOM_SETTING_DEFAULTS.reconnect_backoff_max,
    seek_lock = "yes",
    seek_lock_threshold = ROOM_SETTING_DEFAULTS.seek_lock_threshold,
    auto_force_sync_on_seek = "yes",
    host_seek_threshold = ROOM_SETTING_DEFAULTS.host_seek_threshold,
    host_seek_cooldown = ROOM_SETTING_DEFAULTS.host_seek_cooldown,
}

options.read_options(opts, "mpv-watch")

local sync_on_start = opts.sync_on_start == "yes"
local sync_enabled = false
local last_force_sync_id = nil
local last_track_sync_id = nil
local last_host_command_id = nil
local last_event_id = nil
local last_server_now = nil
local last_server_wall = nil
local applying_remote_seek = false
local applying_remote_pause = false
local force_next_host_apply = false
local last_host_state = nil
local host_buffering_notified = false
local last_time_pos = nil
local last_time_wall = nil
local last_auto_force_sync_at = 0
local host_found_notified = false
local helper_connected = nil  -- nil=never tried, true=ok, false=was ok then lost
local active_poll_until = 0
local reconnect_poll_interval = nil
local reconnect_poll_failures = 0
local runtime_role = nil
local runtime_ready = false
local role_request_in_flight = false
local role_request_callbacks = {}
local observers_registered = false
local heartbeat_timer = nil
local command_timer = nil
local pending_state_timer = nil
local state_request_in_flight = false
local state_update_pending = false
local poll_request_in_flight = false
local sync_request_in_flight = false
local force_sync_request_in_flight = false
local runtime_generation = 0
local last_osd_message = nil
local last_osd_message_at = 0
local poll_commands = nil
local set_sync = nil
local shutdown_cleanup = nil
local send_state = nil
local ensure_runtime_ready = nil
local schedule_command_poll = nil
local prepare_runtime = nil

local function heartbeat_interval()
    return tonumber(opts.heartbeat_interval) or 5.0
end

local function number_option(value, fallback, minimum, maximum)
    local number = tonumber(value) or fallback
    if minimum and number < minimum then
        return minimum
    end
    if maximum and number > maximum then
        return maximum
    end
    return number
end

local function room_number_option(name)
    local limits = ROOM_SETTING_LIMITS[name]
    return number_option(opts[name], ROOM_SETTING_DEFAULTS[name], limits.min, limits.max)
end

local function yes_no(value)
    return value and "yes" or "no"
end

local function command_interval()
    return room_number_option("command_interval")
end

local function idle_command_interval()
    return room_number_option("idle_command_interval")
end

local function active_command_interval()
    return room_number_option("active_command_interval")
end

local function reconnect_backoff_max()
    return room_number_option("reconnect_backoff_max")
end

local function max_reconnect_polls()
    return math.floor(number_option(opts.max_reconnect_polls, 12, 1, 60))
end

local function adaptive_polling_enabled()
    return opts.adaptive_polling == "yes"
end

local function current_command_interval()
    if not adaptive_polling_enabled() then
        return command_interval()
    end
    if helper_connected == false then
        return reconnect_poll_interval or idle_command_interval()
    end
    if mp.get_time() < active_poll_until then
        return active_command_interval()
    end
    return idle_command_interval()
end

local function mark_active_polling(duration)
    if not adaptive_polling_enabled() then
        return
    end
    active_poll_until = math.max(active_poll_until, mp.get_time() + (duration or 6.0))
end

local function stop_heartbeat_timer()
    if heartbeat_timer then
        heartbeat_timer:kill()
        heartbeat_timer = nil
    end
    if pending_state_timer then
        pending_state_timer:kill()
        pending_state_timer = nil
    end
    state_update_pending = false
end

local function ensure_heartbeat_timer()
    if not heartbeat_timer then
        heartbeat_timer = mp.add_periodic_timer(heartbeat_interval(), function() send_state() end)
    end
end

local function start_timers()
    helper_connected = nil
    reconnect_poll_failures = 0
    ensure_heartbeat_timer()
    if not command_timer then
        schedule_command_poll(0)
    end
end

local function stop_timers()
    stop_heartbeat_timer()
    if command_timer then
        command_timer:kill()
        command_timer = nil
    end
    reconnect_poll_interval = nil
    reconnect_poll_failures = 0
    host_buffering_notified = false
end

schedule_command_poll = function(delay)
    if not sync_enabled or command_timer or poll_request_in_flight then
        return
    end
    command_timer = mp.add_timeout(delay or current_command_interval(), function()
        command_timer = nil
        if not sync_enabled then
            return
        end
        poll_commands()
    end)
end

local function should_show_event(event, user_id)
    if not event or not event.message then
        return false
    end
    if event.type == "force_sync" or event.type == "auto_force_sync" then
        return runtime_role == "host"
    end
    if event.type == "sync_to_guest" then
        return false
    end
    if event.type == "guest_buffering" and runtime_role == "host" then
        return false
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

local function show_message(message, duplicate_window)
    message = trim(message)
    if message == "" then
        return
    end
    local now = mp.get_time()
    if message == last_osd_message and now - last_osd_message_at < (duplicate_window or 1.5) then
        return
    end
    last_osd_message = message
    last_osd_message_at = now
    mp.osd_message(OSD_PREFIX .. message)
end

local function helper_args(method, path, body, max_time)
    local args = {
        "curl",
        "-sS",
        "--connect-timeout",
        "1.5",
        "--max-time",
        tostring(max_time or 6),
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
    return args
end

local function parse_helper_result(result)
    if not result or result.status ~= 0 then
        local process_error = result and trim(result.error_string) or ""
        if process_error == "" then
            process_error = result and trim(result.stderr) or ""
        end
        return nil, process_error ~= "" and process_error or "request failed"
    end
    local stdout = result.stdout or ""
    local body, status_text = stdout:match("^(.*)\n(%d%d%d)%s*$")
    local status = tonumber(status_text or "")
    if not status then
        body = stdout
        status = 200
    end

    if body == "" then
        if status >= 400 then
            return nil, "request failed with HTTP " .. status
        end
        return {}, nil
    end

    local parsed, parse_error = utils.parse_json(body)
    if status >= 400 then
        local message = "request failed with HTTP " .. status
        if type(parsed) == "table" and parsed.error then
            message = parsed.error
        end
        return nil, message
    end
    if parsed == nil then
        return nil, parse_error or "invalid helper response"
    end
    return parsed, nil
end

local function helper_command(method, path, body, max_time)
    return {
        -- Use the documented compatibility key for mpv builds that reject
        -- _name in Lua command maps.
        name = "subprocess",
        playback_only = false,
        capture_stdout = true,
        capture_stderr = true,
        capture_size = 1024 * 1024,
        args = helper_args(method, path, body, max_time),
    }
end

local function helper_request(method, path, body, callback)
    return mp.command_native_async(helper_command(method, path, body, 6), function(success, result, error_message)
        if not success then
            callback(nil, error_message or "request failed")
            return
        end
        callback(parse_helper_result(result))
    end)
end

local function finish_role_request(ok, err)
    local callbacks = role_request_callbacks
    role_request_callbacks = {}
    role_request_in_flight = false
    for _, callback in ipairs(callbacks) do
        callback(ok, err)
    end
end

local function load_runtime_role(callback)
    if runtime_role then
        callback(true)
        return
    end

    role_request_callbacks[#role_request_callbacks + 1] = callback
    if role_request_in_flight then
        return
    end

    role_request_in_flight = true
    helper_request("GET", "/api/config", nil, function(result, err)
        if err then
            finish_role_request(false, err)
            return
        end

        local role = trim(result and result.role):lower()
        if role ~= "host" and role ~= "guest" then
            finish_role_request(false, "helper returned an invalid role")
            return
        end

        runtime_role = role
        msg.debug("Loaded helper role: " .. runtime_role)
        finish_role_request(true)
    end)
end

local function helper_request_sync(method, path, body, max_time)
    local result = mp.command_native(helper_command(method, path, body, max_time))
    return parse_helper_result(result)
end

local function post_config(callback)
    helper_request("POST", "/api/config", {
        roomId = opts.room,
        displayName = opts.display_name,
    }, function(result, err)
        if err then
            msg.warn("Failed to save helper config: " .. err)
            if callback then callback(false, err) end
            return
        end
        if result and result.eventId then
            last_event_id = result.eventId
        end
        msg.debug("Saved helper config: room=" .. opts.room .. " displayName=" .. opts.display_name)
        if callback then callback(true, result) end
    end)
end

set_sync = function(enabled, callback, quiet)
    if enabled and not ensure_runtime_ready() then
        sync_enabled = false
        stop_timers()
        if callback then callback(false, "runtime unavailable") end
        return
    end
    if sync_request_in_flight then
        if callback then callback(false, "sync change already in progress") end
        return
    end

    sync_request_in_flight = true
    helper_request("POST", "/api/sync", { enabled = enabled }, function(_, err)
        sync_request_in_flight = false
        if err then
            if enabled then
                sync_enabled = false
                stop_timers()
            end
            if not quiet then
                if enabled and err == "no host found in room" then
                    show_message("Sync disabled: no host found in room")
                elseif enabled then
                    show_message("Sync unavailable: helper unreachable")
                else
                    show_message("Could not turn sync off")
                end
            end
            msg.warn("Failed to set sync: " .. err)
            if callback then callback(false, err) end
            return
        end
        runtime_generation = runtime_generation + 1
        sync_enabled = enabled
        if enabled then
            mark_active_polling(8.0)
            if runtime_role == "guest" then
                force_next_host_apply = true
                host_found_notified = false
            end
            start_timers()
            send_state()
        else
            stop_timers()
            host_found_notified = false
        end
        if not quiet then
            show_message(enabled and "Sync on" or "Sync off")
        end
        if callback then callback(true) end
    end)
end

shutdown_cleanup = function()
    if not sync_enabled then
        stop_timers()
        return
    end
    sync_enabled = false
    stop_timers()
    local _, err = helper_request_sync("POST", "/api/sync", { enabled = false }, 2)
    if err then
        msg.warn("Failed to clean up sync on shutdown: " .. err)
    end
end

local function save_runtime_config(changed_room, callback)
    if not ensure_runtime_ready() then
        if callback then callback(false) end
        return
    end

    local was_synced = sync_enabled
    local function save_config()
        post_config(function(saved)
            if changed_room then
                last_host_state = nil
                host_found_notified = false
                force_next_host_apply = runtime_role == "guest"
                if was_synced and saved then
                    set_sync(true, function(restored)
                        if callback then callback(true, restored) end
                    end, true)
                    return
                end
            elseif saved and sync_enabled and send_state then
                send_state()
            end
            if callback then callback(saved, true) end
        end)
    end

    if changed_room and was_synced then
        set_sync(false, function(disabled)
            if not disabled then
                if callback then callback(false) end
                return
            end
            save_config()
        end, true)
    else
        save_config()
    end
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
    if state_request_in_flight then
        state_update_pending = true
        return
    end
    state_request_in_flight = true
    state_update_pending = false
    local request_generation = runtime_generation
    helper_request("POST", "/api/mpv/state", playback_state(), function(_, err)
        state_request_in_flight = false
        if err then
            msg.warn("Failed to send playback state: " .. err)
        end
        if request_generation ~= runtime_generation then
            state_update_pending = sync_enabled
        end
        if state_update_pending and sync_enabled then
            state_update_pending = false
            send_state()
        end
    end)
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
    if not sync_enabled or not state then
        return
    end
    if runtime_role == "host" and not state.applyToHost then
        return
    end

    if runtime_role ~= "host" and type(state.currentTime) == "number" then
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
        if not host_buffering_notified then
            host_buffering_notified = true
            show_message("Paused because host is buffering")
        end
    else
        host_buffering_notified = false
        mp.set_property_bool("pause", not state.isPlaying)
    end
    applying_remote_pause = false
end

local function force_sync_source_label(force_sync)
    if type(force_sync) ~= "table" then
        return "guest"
    end
    local name = trim(force_sync.sourceDisplayName)
    if name ~= "" then
        return name
    end
    local user_id = trim(force_sync.sourceUserId)
    if user_id ~= "" then
        return user_id
    end
    return "guest"
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
    if not sync_enabled or runtime_role == "host" or not track_sync then
        return
    end

    if track_sync.aid ~= nil and tostring(track_sync.aid) ~= "" then
        mp.set_property("aid", tostring(track_sync.aid))
    end
    if track_sync.sid ~= nil and tostring(track_sync.sid) ~= "" then
        mp.set_property("sid", tostring(track_sync.sid))
    end
    show_message("Tracks updated: audio " .. display_track_id(track_sync.aid) .. ", subtitles " .. display_track_id(track_sync.sid))
end

local function apply_room_settings(settings)
    if type(settings) ~= "table" then
        return
    end

    local polling = settings.polling
    if type(polling) == "table" then
        if type(polling.commandInterval) == "number" then
            opts.command_interval = polling.commandInterval
        end
        if type(polling.adaptivePolling) == "boolean" then
            opts.adaptive_polling = yes_no(polling.adaptivePolling)
        end
        if type(polling.idleInterval) == "number" then
            opts.idle_command_interval = polling.idleInterval
        end
        if type(polling.activeInterval) == "number" then
            opts.active_command_interval = polling.activeInterval
        end
        if type(polling.reconnectBackoffMax) == "number" then
            opts.reconnect_backoff_max = polling.reconnectBackoffMax
        end
    end

    local sync = settings.sync
    if type(sync) == "table" then
        if type(sync.seekLock) == "boolean" then
            opts.seek_lock = yes_no(sync.seekLock)
        end
        if type(sync.seekLockThreshold) == "number" then
            opts.seek_lock_threshold = sync.seekLockThreshold
        end
        if type(sync.autoForceSyncOnSeek) == "boolean" then
            opts.auto_force_sync_on_seek = yes_no(sync.autoForceSyncOnSeek)
        end
        if type(sync.hostSeekThreshold) == "number" then
            opts.host_seek_threshold = sync.hostSeekThreshold
        end
        if type(sync.hostSeekCooldown) == "number" then
            opts.host_seek_cooldown = sync.hostSeekCooldown
        end
    end
end

poll_commands = function()
    if not sync_enabled or poll_request_in_flight then
        return
    end
    poll_request_in_flight = true
    local request_generation = runtime_generation

    local function finish_poll()
        poll_request_in_flight = false
        if sync_enabled then
            schedule_command_poll(current_command_interval())
        end
    end

    helper_request("GET", "/api/mpv/commands", nil, function(data, err)
        if request_generation ~= runtime_generation then
            finish_poll()
            return
        end
        if not sync_enabled then
            poll_request_in_flight = false
            return
        end
        if err or not data then
            reconnect_poll_failures = reconnect_poll_failures + 1
            if helper_connected == true then
                show_message("Connection lost; reconnecting")
            end
            helper_connected = false
            stop_heartbeat_timer()
            if reconnect_poll_failures >= max_reconnect_polls() then
                local failure_count = reconnect_poll_failures
                sync_enabled = false
                stop_timers()
                poll_request_in_flight = false
                show_message("Sync disabled: helper unreachable")
                msg.warn("Helper stayed unreachable after " .. failure_count .. " reconnect polls; sync disabled")
                return
            end
            if adaptive_polling_enabled() then
                local next_interval = reconnect_poll_interval or idle_command_interval()
                reconnect_poll_interval = math.min(next_interval * 1.5, reconnect_backoff_max())
            end
            finish_poll()
            return
        end
        if helper_connected == false then
            show_message("Reconnected")
        end
        helper_connected = true
        reconnect_poll_interval = nil
        reconnect_poll_failures = 0
        ensure_heartbeat_timer()
        apply_room_settings(data.settings)
        if type(data.serverNow) == "number" then
            last_server_now = data.serverNow
            last_server_wall = mp.get_time()
        end
        if data.latestEvent and data.latestEvent.eventId and data.latestEvent.eventId ~= last_event_id then
            last_event_id = data.latestEvent.eventId
            if should_show_event(data.latestEvent, data.userId) then
                show_message(data.latestEvent.message)
            end
        end
        if data.hostCommand and data.hostCommand.commandId and data.hostCommand.commandId ~= last_host_command_id then
            last_host_command_id = data.hostCommand.commandId
            if runtime_role == "host" and data.hostCommand.type == "pause_for_guest_buffering" then
                mp.set_property_bool("pause", true)
                show_message(data.hostCommand.message or "Paused because a guest is buffering")
            end
        end
        if data.syncEnabled ~= nil then
            local was_sync_enabled = sync_enabled
            sync_enabled = data.syncEnabled
            if not sync_enabled then
                stop_timers()
                poll_request_in_flight = false
                if was_sync_enabled then
                    host_found_notified = false
                    if runtime_role == "guest" then
                        show_message("Sync disabled: no host found in room")
                    else
                        show_message("Sync disabled by helper")
                    end
                end
                return
            end
        end
        if data.forceSync and data.forceSync.syncId and data.forceSync.syncId ~= last_force_sync_id then
            last_force_sync_id = data.forceSync.syncId
            mark_active_polling(8.0)
            apply_remote_state(data.forceSync, true)
            if data.forceSync.reason == "auto_seek" then
                show_message("Synced to host seek")
            elseif data.forceSync.reason == "sync_to_guest" then
                show_message("Synced to " .. force_sync_source_label(data.forceSync))
            else
                show_message("Force synced")
            end
            finish_poll()
            return
        end
        if data.trackSync and data.trackSync.syncId and data.trackSync.syncId ~= last_track_sync_id then
            last_track_sync_id = data.trackSync.syncId
            mark_active_polling(6.0)
            apply_track_sync(data.trackSync)
        end
        if data.host then
            local force = force_next_host_apply
            force_next_host_apply = false
            if force then
                mark_active_polling(8.0)
            end
            apply_remote_state(data.host, force)
            if runtime_role == "guest" and sync_enabled and not host_found_notified then
                host_found_notified = true
                show_message(force and "Host found and synced" or "Host found")
            elseif force then
                show_message("Synced to host")
            end
        elseif runtime_role == "guest" and sync_enabled then
            host_found_notified = false
        end
        finish_poll()
    end)
end

local function force_sync(current_time, reason)
    if not ensure_runtime_ready() or force_sync_request_in_flight then
        return
    end

    local state = playback_state()
    mark_active_polling(8.0)
    if type(current_time) == "number" then
        state.currentTime = current_time
    end
    if type(reason) == "string" and reason ~= "" then
        state.reason = reason
    end

    force_sync_request_in_flight = true
    helper_request("POST", "/api/host/force-sync", state, function(result, err)
        force_sync_request_in_flight = false
        if err then
            show_message("Force sync failed")
            msg.warn("Force sync failed: " .. err)
            return
        end
        if result and result.message then
            if result.eventId then
                last_event_id = result.eventId
            end
            show_message(result.message)
        else
            show_message("Force sync sent")
        end
    end)
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
        { label = "Set name", id = "name" },
        { label = "Set room", id = "room" },
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
                    prompt_text_next_tick("Room:", opts.room, function(value)
                        local next_room = trim(value)
                        if next_room ~= "" and next_room ~= opts.room then
                            local previous_room = opts.room
                            opts.room = next_room
                            save_runtime_config(true, function(saved, sync_restored)
                                if saved then
                                    if sync_restored == false then
                                        show_message("Room set to " .. opts.room .. "; sync remains off")
                                    else
                                        show_message("Room set to " .. opts.room)
                                    end
                                else
                                    opts.room = previous_room
                                    show_message("Room update failed")
                                end
                            end)
                        elseif next_room == opts.room then
                            show_message("Room unchanged")
                        end
                    end)
                elseif id == "name" then
                    prompt_text_next_tick("Name:", opts.display_name, function(value)
                        local next_name = trim(value)
                        if next_name ~= "" and next_name ~= opts.display_name then
                            local previous_name = opts.display_name
                            opts.display_name = next_name
                            save_runtime_config(false, function(saved)
                                if saved then
                                    show_message("Name set to " .. opts.display_name)
                                else
                                    opts.display_name = previous_name
                                    show_message("Name update failed")
                                end
                            end)
                        elseif next_name == opts.display_name then
                            show_message("Name unchanged")
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

    show_message("Ctrl+w toggles sync")
    set_sync(not sync_enabled)
end

local function register_observers()
    if observers_registered then
        return
    end
    observers_registered = true

    mp.observe_property("pause", "bool", function(_, paused)
        queue_state_update()
        if paused or not sync_enabled or runtime_role ~= "guest" or applying_remote_pause then
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
        show_message("Paused by host")
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

        if runtime_role == "host" and sync_enabled and opts.auto_force_sync_on_seek == "yes" and last_time_pos and last_time_wall then
            local elapsed = math.max(0, now - last_time_wall)
            local expected_time = last_time_pos + elapsed
            local drift = math.abs(current_time - expected_time)
            local threshold = room_number_option("host_seek_threshold")
            local cooldown = room_number_option("host_seek_cooldown")
            if drift >= threshold and now - last_auto_force_sync_at >= cooldown then
                last_auto_force_sync_at = now
                force_sync(current_time, "auto_seek")
            end
        elseif runtime_role == "guest" and sync_enabled and opts.seek_lock == "yes" and last_host_state then
            local expected_host_time = projected_time(last_host_state)
            local threshold = room_number_option("seek_lock_threshold")
            if expected_host_time and math.abs(current_time - expected_host_time) >= threshold then
                applying_remote_seek = true
                mp.set_property_number("time-pos", expected_host_time)
                applying_remote_seek = false
                show_message("Snapped back to host")
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
    return runtime_ready and runtime_role ~= nil
end

prepare_runtime = function(callback)
    if ensure_runtime_ready() then
        callback(true)
        return
    end

    load_runtime_role(function(loaded, err)
        if not loaded then
            callback(false, err)
            return
        end
        register_observers()
        runtime_ready = true
        callback(true)
    end)
end

-- Lazily push config on the first menu open so startup stays near zero-cost when
-- sync_on_start=no. Once config is pushed (or sync is already on) this is a
-- no-op on every subsequent call.
local config_pushed = false

mp.add_key_binding("Ctrl+w", "mpv-watch-menu", function()
    prepare_runtime(function(ready, err)
        if not ready then
            show_message("Helper unavailable: start it and try again")
            msg.warn("Failed to load helper role: " .. tostring(err))
            return
        end
        if not config_pushed and not sync_enabled then
            config_pushed = true
            post_config()
        end
        show_menu()
    end)
end)

mp.register_event("shutdown", function()
    shutdown_cleanup()
end)

if sync_on_start then
    prepare_runtime(function(ready, err)
        if ready then
            config_pushed = true
            post_config(function(saved)
                if saved then
                    set_sync(true)
                else
                    show_message("Sync unavailable: config update failed")
                end
            end)
        else
            show_message("Sync unavailable: helper unreachable")
            msg.warn("Failed to load helper role: " .. tostring(err))
        end
    end)
end
