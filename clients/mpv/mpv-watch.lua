local mp = require "mp"
local msg = require "mp.msg"
local options = require "mp.options"
local utils = require "mp.utils"

local input_ok, input = pcall(require, "mp.input")

local opts = {
    helper_url = "http://127.0.0.1:8765",
    role = "guest",
    room = "",
    display_name = "mpv watcher",
    sync_on_start = "no",
    state_interval = 1.0,
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
local applying_remote_seek = false
local force_next_host_apply = false
local last_host_state = nil
local last_time_pos = nil
local last_time_wall = nil
local last_auto_force_sync_at = 0
local host_found_notified = false
local poll_commands = nil

local function request(method, path, body)
    local args = {
        "curl",
        "-fsS",
        "-X",
        method,
        "-H",
        "Content-Type: application/json",
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
    if result.stdout == "" then
        return {}, nil
    end
    return utils.parse_json(result.stdout), nil
end

local function post_config()
    local _, err = request("POST", "/api/config", {
        role = opts.role,
        roomId = opts.room,
        displayName = opts.display_name,
    })
    if err then
        msg.warn("Failed to save helper config: " .. err)
    end
end

local function set_sync(enabled)
    sync_enabled = enabled
    local _, err = request("POST", "/api/sync", { enabled = enabled })
    if err then
        mp.osd_message("Watch Together: helper unavailable")
        msg.warn("Failed to set sync: " .. err)
        return
    end
    mp.osd_message(enabled and "Watch Together: sync on" or "Watch Together: sync off")
    if enabled and opts.role == "guest" then
        force_next_host_apply = true
        host_found_notified = false
        if poll_commands then
            poll_commands()
        end
    elseif not enabled then
        host_found_notified = false
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

local function playback_state()
    return {
        currentTime = mp.get_property_number("time-pos", 0),
        isPlaying = not mp.get_property_bool("pause", true),
        isBuffering = is_buffering(),
        duration = mp.get_property_number("duration", 0),
    }
end

local function send_state()
    if not sync_enabled then
        return
    end
    local _, err = request("POST", "/api/mpv/state", playback_state())
    if err then
        msg.warn("Failed to send playback state: " .. err)
    end
end

local function projected_time(state)
    if not state or type(state.currentTime) ~= "number" then
        return nil
    end
    if not state.isPlaying or state.isBuffering or type(state.sampledAt) ~= "number" then
        return state.currentTime
    end
    local elapsed = math.max(0, (os.time() * 1000 - state.sampledAt) / 1000)
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

    if state.isBuffering then
        mp.set_property_bool("pause", true)
        mp.osd_message("Watch Together: host is buffering")
    else
        mp.set_property_bool("pause", not state.isPlaying)
    end
end

poll_commands = function()
    local data, err = request("GET", "/api/mpv/commands")
    if err or not data then
        return
    end
    if data.syncEnabled ~= nil then
        sync_enabled = data.syncEnabled
    end
    if data.forceSync and data.forceSync.syncId and data.forceSync.syncId ~= last_force_sync_id then
        last_force_sync_id = data.forceSync.syncId
        apply_remote_state(data.forceSync, true)
        mp.osd_message("Watch Together: force synced")
        return
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

local function force_sync(current_time)
    local state = playback_state()
    if type(current_time) == "number" then
        state.currentTime = current_time
    end

    local _, err = request("POST", "/api/host/force-sync", state)
    if err then
        mp.osd_message("Watch Together: force sync failed")
        msg.warn("Force sync failed: " .. err)
        return
    end
    mp.osd_message("Watch Together: force sync sent")
end

local function prompt_text(prompt, default, callback)
    if input_ok and input.get then
        input.get({
            prompt = prompt,
            default_text = default or "",
            submit = callback,
        })
        return
    end
    mp.osd_message(prompt .. " requires mp.input support")
end

local function show_menu()
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
                    prompt_text("Room", opts.room, function(value)
                        if value and value ~= "" then
                            opts.room = value
                            post_config()
                            mp.osd_message("Watch Together: room set")
                        end
                    end)
                elseif id == "name" then
                    prompt_text("Display name", opts.display_name, function(value)
                        if value and value ~= "" then
                            opts.display_name = value
                            post_config()
                            mp.osd_message("Watch Together: name set")
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

mp.add_key_binding("Ctrl+w", "mpv-watch-menu", show_menu)

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

    if opts.role == "host" and sync_enabled and opts.auto_force_sync_on_seek == "yes" and last_time_pos and last_time_wall then
        local elapsed = math.max(0, now - last_time_wall)
        local expected_time = last_time_pos + elapsed
        local drift = math.abs(current_time - expected_time)
        local threshold = tonumber(opts.host_seek_threshold) or 2.5
        local cooldown = tonumber(opts.host_seek_cooldown) or 1.5
        if drift >= threshold and now - last_auto_force_sync_at >= cooldown then
            last_auto_force_sync_at = now
            force_sync(current_time)
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

post_config()
set_sync(sync_enabled)
mp.add_periodic_timer(opts.state_interval, send_state)
mp.add_periodic_timer(opts.command_interval, poll_commands)
