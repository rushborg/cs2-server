// RushBMatchTelemetry — пересылает игровые события CS2 (чат + kills +
// round/bomb/mvp) в вебхук RUSH-B.ORG для live-ленты и post-match
// реконструкции.
//
// Архитектурные принципы:
//   1. Raw-first: каждое событие уходит на backend и первым делом
//      сохраняется в get5_raw_events. Потом уже дешифруется в типизированные
//      структуры. Если мы пропустим баг парсинга — данные уцелеют и матч
//      можно переиграть.
//   2. Дубли с MatchZy допустимы: MatchZy шлёт round_end (с полной статой
//      игроков), мы шлём live_round_end (с timing/reason); оба в raw.
//   3. Префикс live_ для всех наших событий — backend разводит по switch и
//      не путает с форматами MatchZy.
//
// Стабильность:
//   - Все хуки возвращают HookResult.Continue — игра идёт штатно
//   - Каждый POST в отдельной Task, таймаут 3 сек, fire-and-forget
//   - Все исключения ловятся внутри плагина, никогда не прорываются в
//     основной поток CS2
//   - Если cvars не выставлены — плагин ничего не шлёт (stateless)
//
// Безопасность:
//   - Bearer-токен per-match в cvar rushborg_chat_webhook_auth
//   - Auth заголовок никогда не попадает в логи
//   - Боты/HLTV отфильтрованы; chat обрезается до 500 символов

using System.Net.Http;
using System.Text;
using System.Text.Json;
using CounterStrikeSharp.API;
using CounterStrikeSharp.API.Core;
using CounterStrikeSharp.API.Modules.Commands;
using CounterStrikeSharp.API.Modules.Cvars;

namespace RushBMatchTelemetry;

public class RushBMatchTelemetry : BasePlugin
{
    public override string ModuleName => "RushB Match Telemetry";
    public override string ModuleVersion => "3.0.0";
    public override string ModuleAuthor => "RUSH-B.ORG";
    public override string ModuleDescription => "Forwards chat + live game events + weapon telemetry to RUSH-B.ORG webhook";

    private const int MessageMaxLength = 500;

    // Один долгоживущий HttpClient на весь процесс — см. рекомендации MSFT,
    // создание на каждый запрос утекает сокеты (TIME_WAIT накапливается).
    private static readonly HttpClient Http = new()
    {
        Timeout = TimeSpan.FromSeconds(3),
    };

    // ─── Per-round weapon aggregation (Phase 3) ─────────────
    // EventWeaponFire + EventPlayerHurt шумные (сотни событий в раунд),
    // поэтому агрегируем локально и шлём один POST на round_end.
    // Всё в main-thread (CSS вызывает все хуки синхронно), без блокировок.
    private sealed class WeaponAgg
    {
        public int ShotsFired;
        public int Hits;
        public int Damage;
        public int HitHead;
        public int HitChest;
        public int HitStomach;
        public int HitArms;
        public int HitLegs;
    }

    // steamid -> weapon -> aggregated counters (current round only)
    private readonly Dictionary<ulong, Dictionary<string, WeaponAgg>> _roundAgg = new();

    // ─── Lifecycle ───────────────────────────────────────────

    public override void Load(bool hotReload)
    {
        // Chat
        AddCommandListener("say", OnSay);
        AddCommandListener("say_team", OnSayTeam);

        // Game events
        RegisterEventHandler<EventPlayerDeath>(OnPlayerDeath);
        RegisterEventHandler<EventRoundStart>(OnRoundStart);
        RegisterEventHandler<EventRoundFreezeEnd>(OnRoundFreezeEnd);
        RegisterEventHandler<EventRoundEnd>(OnRoundEnd);
        RegisterEventHandler<EventBombPlanted>(OnBombPlanted);
        RegisterEventHandler<EventBombDefused>(OnBombDefused);
        RegisterEventHandler<EventBombExploded>(OnBombExploded);
        RegisterEventHandler<EventRoundMvp>(OnRoundMvp);

        // Weapon telemetry (Phase 3) — aggregated, not per-event
        RegisterEventHandler<EventWeaponFire>(OnWeaponFire);
        RegisterEventHandler<EventPlayerHurt>(OnPlayerHurt);
    }

    // ─── Chat hooks ──────────────────────────────────────────

    private HookResult OnSay(CCSPlayerController? player, CommandInfo info)
    {
        TryForwardChat(player, info, teamOnly: false);
        return HookResult.Continue;
    }

    private HookResult OnSayTeam(CCSPlayerController? player, CommandInfo info)
    {
        TryForwardChat(player, info, teamOnly: true);
        return HookResult.Continue;
    }

    private static void TryForwardChat(CCSPlayerController? player, CommandInfo info, bool teamOnly)
    {
        try
        {
            ForwardChat(player, info, teamOnly);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] chat forward error: {ex.GetType().Name}");
        }
    }

    private static void ForwardChat(CCSPlayerController? player, CommandInfo info, bool teamOnly)
    {
        if (player is null || !player.IsValid || player.IsBot || player.IsHLTV)
            return;

        var raw = info.GetArg(1) ?? string.Empty;
        var message = raw.Trim();
        if (message.Length == 0) return;
        if (message[0] == '.' || message[0] == '!') return;

        if (message.Length > MessageMaxLength)
            message = message[..MessageMaxLength];

        var side = SideFromTeamNum(player.TeamNum);

        var payload = Json(new
        {
            @event = teamOnly ? "player_say_team" : "player_say",
            matchid = GetMatchId(),
            steamid = player.SteamID.ToString(),
            name = player.PlayerName ?? string.Empty,
            message,
            side,
        });

        Fire(payload);
    }

    // ─── Game event hooks ────────────────────────────────────

    private HookResult OnPlayerDeath(EventPlayerDeath ev, GameEventInfo info)
    {
        try
        {
            var attacker = ev.Attacker;
            var victim = ev.Userid;
            if (victim is null || !victim.IsValid) return HookResult.Continue;

            bool attackerValid = attacker is { IsValid: true };
            bool isSuicide = !attackerValid || attacker!.SteamID == victim.SteamID;
            // Teamkill: обе стороны валидны, разные игроки, одна команда.
            bool isTeamKill = attackerValid && !isSuicide && attacker!.TeamNum == victim.TeamNum;

            var payload = Json(new
            {
                @event = "live_kill",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                victim_steamid = victim.SteamID.ToString(),
                victim_name = victim.PlayerName ?? string.Empty,
                victim_team = SideFromTeamNum(victim.TeamNum),
                attacker_steamid = attackerValid ? attacker!.SteamID.ToString() : string.Empty,
                attacker_name = attackerValid ? attacker!.PlayerName ?? string.Empty : string.Empty,
                attacker_team = attackerValid ? SideFromTeamNum(attacker!.TeamNum) : string.Empty,
                assister_steamid = ev.Assister is { IsValid: true } ? ev.Assister.SteamID.ToString() : string.Empty,
                weapon = ev.Weapon ?? string.Empty,
                headshot = ev.Headshot,
                penetrated = ev.Penetrated,
                noscope = ev.Noscope,
                through_smoke = ev.Thrusmoke,
                attacker_blind = ev.Attackerblind,
                distance = ev.Distance,
                assisted_flash = ev.Assistedflash,
                is_suicide = isSuicide,
                is_teamkill = isTeamKill,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] player_death error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnRoundStart(EventRoundStart ev, GameEventInfo info)
    {
        try
        {
            var payload = Json(new
            {
                @event = "live_round_start",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                timelimit = ev.Timelimit,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] round_start error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnRoundFreezeEnd(EventRoundFreezeEnd ev, GameEventInfo info)
    {
        try
        {
            var payload = Json(new
            {
                @event = "live_freeze_end",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] freeze_end error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnRoundEnd(EventRoundEnd ev, GameEventInfo info)
    {
        int round = GetCurrentRound();
        try
        {
            var payload = Json(new
            {
                @event = "live_round_end",
                matchid = GetMatchId(),
                round_number = round,
                time = Server.CurrentTime,
                reason = ev.Reason,    // CS2 enum: 1=TargetBombed, 7=CTWin, 8=TWin, 9=Defused, 10=Timeout, 16=Eliminated
                winner = ev.Winner,    // team num: 2=T, 3=CT
                message = ev.Message ?? string.Empty,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] round_end error: {ex.GetType().Name}");
        }
        // Flush accumulated weapon stats AFTER round_end marker (так бэкенд
        // получит сначала границу раунда, потом его итог по оружию).
        // FlushWeaponStats сам ловит свои исключения.
        FlushWeaponStats(round);
        return HookResult.Continue;
    }

    private HookResult OnBombPlanted(EventBombPlanted ev, GameEventInfo info)
    {
        try
        {
            var planter = ev.Userid;
            var payload = Json(new
            {
                @event = "live_bomb_planted",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                player_steamid = planter is { IsValid: true } ? planter.SteamID.ToString() : string.Empty,
                player_name = planter is { IsValid: true } ? planter.PlayerName ?? string.Empty : string.Empty,
                site = ev.Site,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] bomb_planted error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnBombDefused(EventBombDefused ev, GameEventInfo info)
    {
        try
        {
            var defuser = ev.Userid;
            var payload = Json(new
            {
                @event = "live_bomb_defused",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                player_steamid = defuser is { IsValid: true } ? defuser.SteamID.ToString() : string.Empty,
                player_name = defuser is { IsValid: true } ? defuser.PlayerName ?? string.Empty : string.Empty,
                site = ev.Site,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] bomb_defused error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnBombExploded(EventBombExploded ev, GameEventInfo info)
    {
        try
        {
            var payload = Json(new
            {
                @event = "live_bomb_exploded",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                site = ev.Site,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] bomb_exploded error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    private HookResult OnRoundMvp(EventRoundMvp ev, GameEventInfo info)
    {
        try
        {
            var mvp = ev.Userid;
            if (mvp is null || !mvp.IsValid) return HookResult.Continue;

            var payload = Json(new
            {
                @event = "live_round_mvp",
                matchid = GetMatchId(),
                round_number = GetCurrentRound(),
                time = Server.CurrentTime,
                player_steamid = mvp.SteamID.ToString(),
                player_name = mvp.PlayerName ?? string.Empty,
                reason = ev.Reason,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] round_mvp error: {ex.GetType().Name}");
        }
        return HookResult.Continue;
    }

    // ─── Weapon telemetry hooks (Phase 3) ────────────────────
    // Обе функции — hot path (сотни вызовов за раунд). Только lookup в
    // Dictionary + инкремент, никакого сериализации/JSON/HTTP здесь.
    // Flush батча делается в OnRoundEnd.

    private HookResult OnWeaponFire(EventWeaponFire ev, GameEventInfo info)
    {
        try
        {
            var p = ev.Userid;
            if (p is null || !p.IsValid || p.IsBot || p.IsHLTV) return HookResult.Continue;
            GetAgg(p.SteamID, ev.Weapon ?? "").ShotsFired++;
        }
        catch
        {
            // Игнорируем — нельзя ронять hot path
        }
        return HookResult.Continue;
    }

    private HookResult OnPlayerHurt(EventPlayerHurt ev, GameEventInfo info)
    {
        try
        {
            var attacker = ev.Attacker;
            var victim = ev.Userid;
            if (attacker is null || !attacker.IsValid || attacker.IsBot || attacker.IsHLTV) return HookResult.Continue;
            if (victim is null || !victim.IsValid) return HookResult.Continue;
            // Self-damage (HE себе под ноги, falldamage) не считаем в точность
            if (attacker.SteamID == victim.SteamID) return HookResult.Continue;

            var agg = GetAgg(attacker.SteamID, ev.Weapon ?? "");
            agg.Hits++;
            agg.Damage += ev.DmgHealth;
            switch (ev.Hitgroup)
            {
                case 1: agg.HitHead++; break;      // head
                case 2: agg.HitChest++; break;     // chest
                case 3: agg.HitStomach++; break;   // stomach
                case 4: case 5: agg.HitArms++; break;  // l/r arm
                case 6: case 7: agg.HitLegs++; break;  // l/r leg
            }
        }
        catch
        {
            // ignore
        }
        return HookResult.Continue;
    }

    private WeaponAgg GetAgg(ulong steamId, string weapon)
    {
        if (!_roundAgg.TryGetValue(steamId, out var byWeapon))
        {
            byWeapon = new Dictionary<string, WeaponAgg>();
            _roundAgg[steamId] = byWeapon;
        }
        if (!byWeapon.TryGetValue(weapon, out var agg))
        {
            agg = new WeaponAgg();
            byWeapon[weapon] = agg;
        }
        return agg;
    }

    private void FlushWeaponStats(int round)
    {
        if (_roundAgg.Count == 0) return;
        try
        {
            var entries = new List<object>();
            foreach (var (sid, byWeapon) in _roundAgg)
            {
                foreach (var (weapon, a) in byWeapon)
                {
                    entries.Add(new
                    {
                        steamid = sid.ToString(),
                        weapon,
                        shots_fired = a.ShotsFired,
                        hits = a.Hits,
                        damage = a.Damage,
                        hit_head = a.HitHead,
                        hit_chest = a.HitChest,
                        hit_stomach = a.HitStomach,
                        hit_arms = a.HitArms,
                        hit_legs = a.HitLegs,
                    });
                }
            }
            var payload = Json(new
            {
                @event = "live_weapon_stats",
                matchid = GetMatchId(),
                round_number = round,
                time = Server.CurrentTime,
                entries,
            });
            Fire(payload);
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] flush_weapon error: {ex.GetType().Name}");
        }
        finally
        {
            _roundAgg.Clear();
        }
    }

    // ─── HTTP transport ──────────────────────────────────────

    private static void Fire(string payload)
    {
        var url = ConVar.Find("rushborg_chat_webhook_url")?.StringValue;
        var auth = ConVar.Find("rushborg_chat_webhook_auth")?.StringValue;
        var matchId = ConVar.Find("rushborg_match_id")?.StringValue;

        // matchid=empty означает либо прогрев перед загрузкой матча, либо
        // idle сервер. В обоих случаях события никому не нужны — экономим
        // HTTP и не засоряем raw-архив бэкенда.
        if (string.IsNullOrWhiteSpace(url) ||
            string.IsNullOrWhiteSpace(auth) ||
            string.IsNullOrWhiteSpace(matchId))
            return;

        _ = SendAsync(url!, auth!, payload);
    }

    private static async Task SendAsync(string url, string auth, string payload)
    {
        try
        {
            using var req = new HttpRequestMessage(HttpMethod.Post, url);
            req.Headers.TryAddWithoutValidation("Authorization", auth);
            req.Content = new StringContent(payload, Encoding.UTF8, "application/json");

            using var res = await Http.SendAsync(req).ConfigureAwait(false);
            if (!res.IsSuccessStatusCode)
            {
                Console.WriteLine($"[RushB-Telemetry] webhook non-2xx: {(int)res.StatusCode}");
            }
        }
        catch (TaskCanceledException)
        {
            // Таймаут — норма при сетевых глитчах, не спамим
        }
        catch (HttpRequestException ex)
        {
            Console.WriteLine($"[RushB-Telemetry] network error: {ex.Message}");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-Telemetry] post failed: {ex.GetType().Name}");
        }
    }

    // ─── Helpers ─────────────────────────────────────────────

    private static readonly JsonSerializerOptions JsonOpts = new()
    {
        // Игнорируем нули/пустые строки по требованию можно включить позже.
        PropertyNamingPolicy = null,
    };

    private static string Json(object payload) => JsonSerializer.Serialize(payload, JsonOpts);

    private static string GetMatchId()
    {
        return ConVar.Find("rushborg_match_id")?.StringValue ?? string.Empty;
    }

    // CS2 round counter not directly exposed; use game rules round number.
    // Fallback to 0 if rules entity not ready (pre-warmup / map change).
    private static int GetCurrentRound()
    {
        try
        {
            var rules = Utilities.FindAllEntitiesByDesignerName<CCSGameRulesProxy>("cs_gamerules").FirstOrDefault();
            return rules?.GameRules?.TotalRoundsPlayed ?? 0;
        }
        catch
        {
            return 0;
        }
    }

    private static string SideFromTeamNum(int teamNum) => teamNum switch
    {
        2 => "t",
        3 => "ct",
        1 => "spec",
        _ => string.Empty,
    };
}
