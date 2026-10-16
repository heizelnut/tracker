# Ricomposizione — Progress Tracker

Progetto inizializzato a partire dalla conversazione ChatGPT
[_"Programma palestra 3 giorni"_](https://chatgpt.com/share/6ac499e1-17a8-83eb-89f8-8048abe883e5).

L'obiettivo è la **ricomposizione corporea**: costruire/recuperare massa muscolare
riducendo gradualmente il grasso, con un deficit calorico moderato.

## Cosa contiene

| File | Descrizione |
|---|---|
| [`index.html`](./index.html) | App web (single file) per tracciare peso, calorie e macro. Salva tutto in **LocalStorage**. |
| [`cmd/server`](./cmd/server) | Backend minimale (Go, stdlib) che serve l'app, sincronizza un blob opaco per chiave ed espone un server **MCP** per gli agenti. |
| [`Dockerfile`](./Dockerfile) · [`docker-compose.yml`](./docker-compose.yml) | Self-hosting in un container. |
| [`data/cibi.csv`](./data/cibi.csv) | Database alimenti d'esempio, esportato dal foglio **“Cibo”** di `~/Documents/dieta/macros.xlsx`. |
| [`data/allenamenti.csv`](./data/allenamenti.csv) | Scheda allenamento 3 giorni d'esempio (dalla conversazione originale). |
| [`docs/scheda-allenamento.md`](./docs/scheda-allenamento.md) | Scheda palestra 3 giorni (2× upper / 2× lower). |
| [`docs/piano-nutrizionale.md`](./docs/piano-nutrizionale.md) | Target calorici e macro (giorni allenamento vs riposo). |

## Profilo di riferimento

- 22 anni, maschio
- 172 cm, 72,7 kg
- Vita sedentaria + 3 allenamenti/settimana
- ~25 min di cardio per sessione (15' prima + 10' dopo)
- Obiettivo: ricomposizione **abbastanza aggressiva**

## Come usare il tracker

Apri `index.html` nel browser (anche da smartphone Android). Puoi servirlo
localmente con:

```bash
# Python
python3 -m http.server 8000

# oppure Node
npx serve .
```

Poi visita <http://localhost:8000>.

### Funzionalità

L'interfaccia è divisa in **tab**:

- **Dashboard** — peso attuale, medie 7 giorni (peso, kcal, P/G/C), nuova registrazione, grafico del peso.
- **Diario** — alimenti consumati per data, con grammi e totali kcal/macro vs target. Le voci si possono **riordinare** (trascina l'handle `⠿` oppure usa `▲`/`▼`).
- **Allenamenti** — programma diviso per sessioni, aggiungi/modifica manualmente o importa da CSV.
- **Cibi** — database alimenti: aggiungi/modifica manualmente, import/export CSV, ricerca e filtro per categoria.
- **Storico** — tutte le registrazioni con peso, kcal, macro e tipo giornata.

In dettaglio:

- 📊 Andamento del peso con grafico
- ⚖️ Peso giornaliero
- 🔥 Calorie introdotte
- 🏋️ Distinzione allenamento / riposo
- 📈 Medie degli ultimi 7 giorni (peso, kcal, proteine, grassi, carboidrati)
- 🗂️ Storico completo con macro
- 💾 Salvataggio automatico in LocalStorage
- 🗑️ Eliminazione singola registrazione o di tutto lo storico
- 📱 Interfaccia responsive per Android
- 🥩 **Database cibi** importabile da CSV (vedi sotto)
- ➕ **Aggiunta/modifica manuale degli alimenti** dall'interfaccia
- 📓 **Diario alimentare**: aggiungi gli alimenti con i grammi e l'app calcola kcal + macro vs target
- ↕️ **Riordino voci del diario**: trascina l'handle `⠿` (desktop) o usa i pulsanti `▲`/`▼` (anche su mobile) per cambiare l'ordine dei pasti nella giornata
- 🤖 **Suggerimento greedy**: cosa mangiare nel resto della giornata per completare kcal e macro

> I dati restano **sul dispositivo/browser** e non vengono inviati a nessun server.

## ➕ Aggiungere un alimento a mano

Nel tab **Cibi**, il form in alto permette di creare un alimento con:

| Campo | Note |
|---|---|
| Nome | obbligatorio |
| Proteine / Grassi / Carboidrati | g per 100 g |
| kcal | se lasciato vuoto viene calcolata da `P×4 + C×4 + G×9` |
| Min / Max | porzione consigliata in grammi (opzionale) |
| Categoria | tag separati da virgola, es. `proteina,latticino` |
| Pezzo | grammi per pezzo (opzionale) |

Ogni riga della tabella ha i pulsanti **Aggiungi** (al diario), **Modifica** e **×** (elimina).

## 🥩 Importare un CSV di cibi

Nel database cibi puoi **importare un CSV** con la stessa struttura del secondo
foglio (`Cibo`) di `~/Documents/dieta/macros.xlsx`:

```csv
Food (100gr);Proteine;Grasso;Carboidrati;kcal;Min (g);Max (g);Categoria;Pezzo (g)
Grana Padano;33;29;0;398;10;30;extra,secco;
Petto di pollo;22,5;2,6;0;120;120;280;proteina;
```

- I valori sono **per 100 g**.
- Delimitatore `,`, `;` o tab: rilevato automaticamente.
- Decimali con `.` o `,`.
- L'intestazione viene riconosciuta anche se il file ha un preambolo (es. `macros.csv`).
- Se `kcal` manca viene calcolata con `P×4 + C×4 + G×9`.
- Le colonne `Min`, `Max`, `Categoria` e `Pezzo` sono opzionali.
- L'import **unisce** i dati: alimenti con lo stesso nome vengono aggiornati, gli altri aggiunti.

Il pulsante **“Carica alimenti di esempio”** inserisce i 32 alimenti di
[`data/cibi.csv`](./data/cibi.csv) senza bisogno di importare nulla.
Puoi anche **esportare** il database corrente o **scaricare un modello** CSV vuoto.

## 🏋️ Allenamenti (CSV)

Il tab **Allenamenti** gestisce la scheda divisa per **sessioni** (giorni). Ogni
esercizio ha: Sessione, Esercizio, Serie, Ripetizioni, Recupero, Note — tutti
testi liberi, così funzionano sia `3 × 6-10` sia `3 × 30-60"`.

- **Carica programma di esempio** inserisce la scheda 3 giorni della conversazione
  originale (22 esercizi, `data/allenamenti.csv`).
- **Importa CSV** con questa struttura:

```csv
Sessione;Esercizio;Serie;Ripetizioni;Recupero;Note
Giorno 1 — Upper A;Spinta manubri — panca piana;3;6-10;2-3';
Giorno 1 — Upper A;Alzate laterali;3;12-15;60-90";
```

- Delimitatore `,` o `;` auto-rilevato; intestazione riconosciuta automaticamente.
- Colonne `Sessione` e `Note` opzionali.
- L'import **unisce**: stessa sessione + esercizio = aggiorna, altrimenti aggiunge.
- Puoi anche **esportare** il programma o scaricare un **modello** CSV.
- **⏱ Timer recupero fullscreen**: ogni esercizio ha un pulsante che avvia un countdown
a schermo intero col tempo di recupero ricavato dalla colonna *Recupero*
(`2-3'` → 180s, `60-90"` → 90s, `1'30"` → 90s). Include pausa, ±15s, anello di
progresso, beep e vibrazione a fine recupero. Esc/Chiudi per uscire.

## 🤖 Algoritmo greedy (score per macro)

Nel tab **Diario**, sotto i totali, l'app suggerisce **cosa mangiare per completare
la giornata** valutando **tutta la tabella dei cibi**:

1. Calcola il fabbisogno residuo `target − consumato` per kcal, proteine, grassi e carboidrati.
2. Per ogni alimento, con la sua porzione consigliata (`Max`, altrimenti `Min`, altrimenti 100 g),
   calcola uno **score per macro**: quanto la porzione copre il residuo di quel macro
   (100% = lo copre esattamente; chi sfora viene penalizzato con `1/ratio`).
3. Combina gli score in un **punteggio totale**, pesando di più i macro più carenti e in
   particolare le proteine (pesi: P 0.40, C 0.25, G 0.20, kcal 0.15).
4. Ordina e restituisce i **primi 10** alimenti, con la possibilità di riordinare per
   singolo macro (proteine / grassi / carboidrati / calorie) ed escludere i cibi già nel diario.

Ogni riga mostra lo score e i grammi apportati per ciascun macro, più un pulsante
**Aggiungi** per inserire la porzione nel diario.

## 🐳 Self-hosting (docker compose)

Il repo include un backend **volutamente stupido**: serve l'app e salva un
**blob opaco** per chiave su disco. Niente database, niente schema, niente
autenticazione — è pensato per girare in **LAN / dietro Tailscale**, non esposto
su internet. Il blob resta opaco: l'unica eccezione è il server **MCP**, che sa
leggere e scrivere alimenti e allenamenti su richiesta di un agente.

### Avvio rapido

```bash
cp .env.example .env
docker compose up --build
# apri http://localhost:8080
```

### API

| Metodo | Path | Descrizione |
|---|---|---|
| `GET` | `/api/kv/{key}` | Legge il blob (header `X-Updated-At` col timestamp) |
| `PUT` | `/api/kv/{key}` | Scrive il blob (max 8 MiB, anche gzip) |
| `DELETE` | `/api/kv/{key}` | Elimina il blob |
| `POST` | `/mcp` | Server MCP (Streamable HTTP, JSON-RPC 2.0) per gli agenti |
| `GET` | `/healthz` | Healthcheck |
| `*` | `/` | Serve l'app statica (`index.html`) |

I blob finiscono in `DATA_DIR` (un file per chiave, scrittura atomica
temp+rename). Le chiavi sono `[A-Za-z0-9_-]{1,64}`.

#### Compressione gzip

Il diario è JSON molto ripetitivo: un backup reale (~320 KiB) si comprime
~20-25x, quindi la compressione è quella che rende la sync veloce.

- **Upload**: l'app comprime il body con `CompressionStream` e manda
  `Content-Encoding: gzip`; se il browser non lo supporta, manda JSON normale.
- **Download**: se il client manda `Accept-Encoding: gzip` la risposta è
  compressa (`Vary: Accept-Encoding`).
- **Il server resta stupido**: non sa cosa c'è dentro il blob, controlla solo i
  magic bytes `1f 8b` per capire se i byte su disco sono già gzip. Un blob
  salvato compresso viene servito così com'è a chi accetta gzip (zero CPU) e
  decompresso al volo per chi non lo accetta (`curl`, client vecchi).
- Il limite di 8 MiB vale sia sul body ricevuto sia sui byte decompressi, così
  un blob piccolo non può espandersi in una risposta enorme.

### Sync nell'app

In fondo all'app c'è la card **“Sincronizzazione tra dispositivi”**: imposta
l'URL del server (vuoto = stesso server che serve l'app), una **chiave**
(default `default`) e attiva **Auto-sync**.

La sincronizzazione è **per record**, non a blob intero:

- ogni record (peso/calorie per data, giornata di diario, alimento o esercizio
  per id, singola spunta di allenamento) porta un timestamp di ultima modifica;
- al sync l'app scarica il blob, **unisce** locale e remoto record per record
  (vince la modifica più recente) e rispedisce solo se serve;
- un dispositivo rimasto indietro **non può più cancellare** le giornate
  registrate su un altro dispositivo: le unisce;
- le eliminazioni viaggiano come *tombstone*, così non vengono resuscitate da
  una copia vecchia.

L'invio è automatico e resiliente:

- debounce breve su ogni modifica, con **retry** a backoff (3 s → max 5 min);
- si riattiva quando il dispositivo torna **online**, quando l'app torna in
  primo piano e ogni minuto mentre è visibile;
- un tentativo best-effort viene fatto anche alla chiusura della pagina
  (`pagehide`, con `keepalive`);
- la card mostra quante **modifiche restano da inviare** e l'esito dell'ultimo
  tentativo.

Il server resta un **blob store opaco**: non conosce lo schema, è l'app a
eseguire il merge.

### MCP (per gli agenti)

Lo stesso processo espone un server **MCP** (Model Context Protocol, Streamable
HTTP) su `POST /mcp`, così un agente può **ispezionare e ampliare** il database
alimenti, il programma allenamenti e il diario alimentare. Niente secondo
processo né dipendenze: è JSON-RPC 2.0 scritto con la sola stdlib.

| Tool | Cosa fa |
|---|---|
| `tracker_summary` | Conteggi, sessioni, categorie, ultime registrazioni, target |
| `list_foods` | Elenca e cerca i cibi (per nome e categoria) |
| `add_food` | Aggiunge o aggiorna un alimento (upsert per nome) |
| `list_workouts` | Elenca gli esercizi (filtro per sessione/nome) |
| `add_workout` | Aggiunge o aggiorna un esercizio (upsert per sessione+nome) |
| `list_diary` | Legge il diario: un giorno completo o l'elenco dei giorni recenti |
| `add_diary_item` | Aggiunge un alimento a un giorno (macro calcolate dai grammi) |
| `update_diary_item` | Cambia i grammi di una voce e ricalcola le macro |
| `remove_diary_item` | Rimuove una voce dal giorno |
| `set_diary_type` | Segna il giorno come allenamento o riposo |

Le scritture usano lo **stesso merge per record** dell'app: il server legge il
blob, aggiorna il timestamp in `meta.updated` e riscrive solo la collezione
toccata, lasciando intatti `entries`, `done` e i giorni di diario non toccati.
Al sync successivo il dispositivo vede il nuovo cibo, esercizio o giornata come
modifica più recente e la unisce, senza perdere le proprie.

Configurazione in un client MCP (esempio con [pi](https://pi.dev)):

```json
{
  "mcpServers": {
    "tracker": { "url": "http://tracker.lab.heiz.cc/mcp" }
  }
}
```

Da riga di comando:

```bash
pi mcp add tracker --url http://localhost:8080/mcp
```

Ogni tool accetta un parametro opzionale `key` per scegliere il blob di sync
(default `default`).

### Sviluppo

```bash
make run      # :8080, serve ./index.html, blob in ./data
make test     # test Go + JS (merge/sync per-record)
make test-js  # solo i test JS del sync
make image REGISTRY=registry.lab.heiz.cc   # build + push (linux/arm64)
```

### Homelab

Lo stack per l'homelab è in `~/code/nix` → [`hosts/lab/docker/tracker/`](../nix/hosts/lab/docker/tracker)
(Traefik + registry privato, `tracker.lab.heiz.cc`).

## 🔄 Alternativa senza server: backup JSON

Se non vuoi self-hostare, in fondo all'app ci sono **Esporta backup (.json)** e
**Importa backup**: sposti il file con iCloud Drive / Google Drive / AirDrop /
WhatsApp. Contiene peso/calorie, diario, database cibi e programma allenamenti.

## Prossimi passi possibili

- [x] Tracciamento macro giornalieri (proteine, carboidrati, grassi)
- [ ] Promemoria settimanale per registrare peso e calorie
- [ ] Export dei dati (peso, calorie, diario) in formato CSV
