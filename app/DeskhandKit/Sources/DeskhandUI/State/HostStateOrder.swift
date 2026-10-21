import Foundation

/// Orders each host's connection state between `host.list` replies and the events that carry it
/// (`connection.state`, `host.added`, `host.updated`). The daemon raises a host's
/// `connection_generation` with every new connection, so state of a smaller generation is older.
/// Within one generation the events arrive in order, and a state event that arrived after a
/// `host.list` was asked for is at least as new as that reply. Generations start over when the
/// daemon restarts; `forget()` drops what is held so the next reply and events are taken as they
/// are.
struct HostStateOrder {
    /// The generation of the state the store holds, per host.
    private var generations: [String: Int] = [:]
    /// `mark` when the last state event of each host was applied.
    private var lastEvent: [String: Int] = [:]
    /// Counts the applied state events.
    private(set) var mark = 0

    /// A state event of `hostID` at `generation` is not older than the held state.
    func acceptsEvent(_ hostID: String, generation: Int) -> Bool {
        generation >= (generations[hostID] ?? .min)
    }

    mutating func recordEvent(_ hostID: String, generation: Int) {
        mark += 1
        lastEvent[hostID] = mark
        generations[hostID] = generation
    }

    /// A `host.list` reply asked for when `mark` was `requested` replaces the held state of
    /// `hostID`: always, unless a state event arrived since the request and the reply's
    /// `generation` is not newer than that event's.
    func replyWins(_ hostID: String, generation: Int, requested: Int) -> Bool {
        guard let event = lastEvent[hostID], event > requested, let held = generations[hostID] else { return true }
        return generation > held
    }

    mutating func recordReply(_ hostID: String, generation: Int) {
        generations[hostID] = generation
    }

    mutating func remove(_ hostID: String) {
        generations[hostID] = nil
        lastEvent[hostID] = nil
    }

    /// Drops the held generations, after the daemon connection came back.
    mutating func forget() {
        generations = [:]
        lastEvent = [:]
    }
}
