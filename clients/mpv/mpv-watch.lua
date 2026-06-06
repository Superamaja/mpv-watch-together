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
}

options.read_options(opts, "mpv-watch")

local sync_enabled = opts.sync_on_start == "yes"
local last_force_sync_id = nil
local applying_remote_seek = false

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
end

local function playback_state()
    return {
        currentTime = mp.get_property_number("time-pos", 0),
        isPlaying = not mp.get_property_bool("pause", true),
        isBuffering = mp.get_property_bool("core-idle", false),
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

local function poll_commands()
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
    apply_remote_state(data.host, false)
end

local function force_sync()
    local _, err = request("POST", "/api/host/force-sync", {})
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
    local items = {
        { "Toggle sync", "toggle" },
        { "Set room", "room" },
        { "Set display name", "name" },
        { "Force sync", "force" },
    }

    if input_ok and input.select then
        input.select({
            prompt = "Watch Together",
            items = items,
            submit = function(id)
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
                elseif id == "force" then
                    force_sync()
                end
            end,
        })
    else
        mp.osd_message("Watch Together: Ctrl+w toggles sync")
        set_sync(not sync_enabled)
    end
end

mp.add_key_binding("Ctrl+w", "mpv-watch-menu", show_menu)

mp.observe_property("time-pos", "number", function()
    if applying_remote_seek then
        return
    end
end)

post_config()
set_sync(sync_enabled)
mp.add_periodic_timer(opts.state_interval, send_state)
mp.add_periodic_timer(opts.command_interval, poll_commands)
