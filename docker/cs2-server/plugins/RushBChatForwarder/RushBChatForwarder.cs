// RushBChatForwarder — пересылает игровой чат CS2 в вебхук RUSH-B.ORG.
//
// MatchZy сам не отдаёт player_say через matchzy_remote_log_url, поэтому
// этот плагин закрывает пробел: хукает стандартные команды `say` и
// `say_team`, собирает {steamid, name, message, side} и шлёт POST на
// бэкенд с тем же форматом Bearer-токена, что и прочие webhook-события.
//
// Стабильность:
//   - все хуки возвращают HookResult.Continue (чат в игре идёт дальше штатно)
//   - HTTP запрос fire-and-forget в отдельной Task, с таймаутом 3 сек
//   - любое исключение ловится и только логируется в консоль сервера
//   - боты, HLTV, пустые сообщения и MatchZy-команды (".", "!") пропускаются
//
// Безопасность:
//   - cvars rushborg_chat_webhook_url / rushborg_chat_webhook_auth /
//     rushborg_match_id выставляются бэкендом при загрузке матча и сбрасываются
//     при завершении; если какое-то пустое — плагин ничего не шлёт
//   - сообщение обрезается до 500 символов перед отправкой
//   - auth-заголовок не попадает в логи ни при каких ошибках

using System.Net.Http;
using System.Text;
using System.Text.Json;
using CounterStrikeSharp.API;
using CounterStrikeSharp.API.Core;
using CounterStrikeSharp.API.Modules.Commands;
using CounterStrikeSharp.API.Modules.Cvars;

namespace RushBChatForwarder;

public class RushBChatForwarder : BasePlugin
{
    public override string ModuleName => "RushB Chat Forwarder";
    public override string ModuleVersion => "1.0.0";
    public override string ModuleAuthor => "RUSH-B.ORG";
    public override string ModuleDescription => "Forwards in-game chat to RUSH-B.ORG webhook";

    private const int MessageMaxLength = 500;

    // Один долгоживущий HttpClient на весь процесс — см. рекомендации MSFT,
    // создание на каждый запрос утекает сокеты (TIME_WAIT накапливается).
    private static readonly HttpClient Http = new()
    {
        Timeout = TimeSpan.FromSeconds(3),
    };

    public override void Load(bool hotReload)
    {
        AddCommandListener("say", OnSay);
        AddCommandListener("say_team", OnSayTeam);
    }

    private HookResult OnSay(CCSPlayerController? player, CommandInfo info)
    {
        TryForward(player, info, teamOnly: false);
        return HookResult.Continue;
    }

    private HookResult OnSayTeam(CCSPlayerController? player, CommandInfo info)
    {
        TryForward(player, info, teamOnly: true);
        return HookResult.Continue;
    }

    private static void TryForward(CCSPlayerController? player, CommandInfo info, bool teamOnly)
    {
        try
        {
            Forward(player, info, teamOnly);
        }
        catch (Exception ex)
        {
            // Никогда не даём исключениям из пересылки уронить основной чат.
            Console.WriteLine($"[RushB-ChatForwarder] unexpected error: {ex.GetType().Name}");
        }
    }

    private static void Forward(CCSPlayerController? player, CommandInfo info, bool teamOnly)
    {
        if (player is null || !player.IsValid || player.IsBot || player.IsHLTV)
            return;

        var url = ConVar.Find("rushborg_chat_webhook_url")?.StringValue;
        var auth = ConVar.Find("rushborg_chat_webhook_auth")?.StringValue;
        var matchId = ConVar.Find("rushborg_match_id")?.StringValue;

        if (string.IsNullOrWhiteSpace(url) ||
            string.IsNullOrWhiteSpace(auth) ||
            string.IsNullOrWhiteSpace(matchId))
        {
            return;
        }

        var raw = info.GetArg(1) ?? string.Empty;
        var message = raw.Trim();
        if (message.Length == 0)
            return;

        // MatchZy-команды и admin-префиксы обрабатывает сам MatchZy.
        // В ленту сайта им не место (команды не для других игроков).
        if (message[0] == '.' || message[0] == '!')
            return;

        if (message.Length > MessageMaxLength)
            message = message[..MessageMaxLength];

        var side = player.TeamNum switch
        {
            2 => "t",     // TERRORIST
            3 => "ct",    // CT
            1 => "spec",
            _ => string.Empty,
        };

        var payload = JsonSerializer.Serialize(new ChatPayload
        {
            Event = teamOnly ? "player_say_team" : "player_say",
            MatchId = matchId!,
            SteamId = player.SteamID.ToString(),
            Name = player.PlayerName ?? string.Empty,
            Message = message,
            Side = side,
        });

        // Fire-and-forget: не ждём ответа, не блокируем тик сервера.
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
                Console.WriteLine($"[RushB-ChatForwarder] webhook non-2xx: {(int)res.StatusCode}");
            }
        }
        catch (TaskCanceledException)
        {
            // таймаут — норма при временных проблемах с сетью, не спамим
        }
        catch (HttpRequestException ex)
        {
            Console.WriteLine($"[RushB-ChatForwarder] network error: {ex.Message}");
        }
        catch (Exception ex)
        {
            Console.WriteLine($"[RushB-ChatForwarder] post failed: {ex.GetType().Name}");
        }
    }

    private sealed class ChatPayload
    {
        [System.Text.Json.Serialization.JsonPropertyName("event")]
        public required string Event { get; init; }

        [System.Text.Json.Serialization.JsonPropertyName("matchid")]
        public required string MatchId { get; init; }

        [System.Text.Json.Serialization.JsonPropertyName("steamid")]
        public required string SteamId { get; init; }

        [System.Text.Json.Serialization.JsonPropertyName("name")]
        public required string Name { get; init; }

        [System.Text.Json.Serialization.JsonPropertyName("message")]
        public required string Message { get; init; }

        [System.Text.Json.Serialization.JsonPropertyName("side")]
        public required string Side { get; init; }
    }
}
