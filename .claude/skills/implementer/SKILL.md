---
name: implementer
description: Ajouter ou faire évoluer un module, un cas d'usage, une commande ou un job de koffr en respectant l'architecture, le TDD et la Definition of done — structure d'un module Go, exemples canoniques cités dans le code vivant, pièges connus. À utiliser pour toute tâche de code.
---

# Implémenter — Go 1.27, sans CGO

Ce skill est **thématique et au présent** : pas de « leçons du lot N », pas d'historique. Les
références de vérité sont `CLAUDE.md`, `ARCHITECTURE.md` et `docs/adr/` ; ce skill montre comment
les appliquer. Les pièges de la pile vivent dans `CLAUDE.md` § Conventions et ne sont pas répétés
ici.

## Avant d'écrire

1. Lire `ROADMAP.md` (lot en cours), le plan, et le `rules.md` du module visé.
2. Lire l'exigence `E-nnn` **et son § du CDC**. Ce qu'il ne dit pas se demande
   (`docs/questions.md`), ne s'invente pas.
3. **Mesurer avant de modéliser** si une donnée existe.
4. Écrire la règle `MOD-nn` dans `rules.md` **avant** le code : énoncé, source, nom du test.
5. **TDD**, sans exception pour le domaine et les adaptateurs qui portent une garantie.

## TDD : le geste, ici

1. **Nommer le test depuis la règle.** Le numéro de la règle est **dans le nom de la fonction** :
   `func TestCFG01UnknownKeyIsAnErrorNamingTheKeyAndItsLine(t *testing.T)`. Un test sans numéro est
   un test dont on ne sait pas ce qu'il prouve. Les sous-cas passent par `t.Run`.
2. **Écrire le test d'abord**, avec ses trois cas quand ils existent : nominal, limite, erreur
   typée. Sur une base SQLite réelle si le module écrit — `t.TempDir()`, jamais de simulacre de
   base.
3. **Lancer et constater le rouge** : `go test ./internal/<paquet>/ -run TestXxx`. Le rouge doit
   échouer **pour la bonne raison**. Coller la ligne d'échec dans la conversation.
4. **Écrire le code minimal.** Pas d'anticipation d'un cas qui n'a pas son test.
5. **Vert**, même commande, puis refactor, le test restant vert.
6. **Élargir** : le cas suivant, ou la règle suivante.

**Quand le code existe déjà** — un schéma écrit à la tâche précédente, par exemple — le rouge par
antériorité est impossible. On le **dit**, et on prouve au moins que le test mord : retirer la
contrainte, constater l'échec, la remettre. Un rouge par suppression vaut moins ; ne pas le faire
passer pour un rouge.

**Un test qui ne peut pas échouer ne prouve rien.** Avant de le croire, se demander ce qui le
ferait tomber — et le vérifier. Deux pièges vus en vrai :

- le test s'exécute sur un document ou un état où le cas **n'existe pas** (vérifier l'absence d'une
  clé dans une configuration qui n'a aucune destination : il passe, et il ne teste rien) ;
- le test garde un chemin que **personne n'emprunte** (« aucune connexion sortante » est vrai tant
  que rien n'ouvre de connexion). Câbler le chemin, ou dire que la garde est vide.

**Une règle de lint qu'on assouplit s'accompagne de la garde qui la remplace, le jour même.**
ADR-0013 a ouvert `database/sql` à `internal/engine` ; `internal/engine/queries_test.go` lit les
littéraux SQL du paquet et refuse tout ce qui n'est pas la requête de version. Sans cela, on a
retiré un garde-fou et écrit une phrase à la place.

Le geste : une **fixture violante** quand c'est possible — `internal/arch/testdata/` en contient une
par règle —, sinon retirer ce que le test vérifie et constater qu'il tombe.

**Pour retirer puis remettre, copier le fichier, jamais `git checkout`.** `git checkout <fichier>`
restaure depuis l'**index** : sur un fichier jamais indexé, il ne remet pas ce qu'on avait, il
remet une version antérieure — ou rien. `cp fichier /tmp/x && … && cp /tmp/x fichier`.
Et relancer avec **`-count=1`** : le cache de `go test` rend « ok » un paquet qu'on vient de casser.

Ce qu'on ne fait pas : écrire le code puis « ajouter les tests » ; adapter le test au code quand il
échoue ; passer un test en `t.Skip` pour merger.

## Structure d'un module du domaine

```
internal/domain/<module>/
  doc.go        # une phrase : ce que le module décide
  model.go      # entrées, sorties, invariants. Types du domaine, pas de types SQL ni HTTP
  ports.go      # les interfaces dont le module a besoin : ToolFinder, Store, Clock, Notifier
  <usecase>.go  # un cas d'usage par fichier, nommé par ce qu'il fait
  errors.go     # erreurs typées, comparables par errors.Is
  rules.md      # les règles MOD-nn : énoncé, source, test
  *_test.go     # un test par règle, à côté du code
```

**Un module ne connaît pas son voisin** (`AR-03`). La sauvegarde a besoin de la résolution, du
catalogue et des alertes : elle les déclare comme **ports chez elle**, et `cmd/koffr` câble les
implémentations. C'est plus verbeux, et c'est ce qui rend le domaine testable sans base, sans
réseau et sans outil externe.

Les adaptateurs (`engine`, `store`, `pipeline`, `state`, `egress`, `obs`, `httpd`, `cli`) **n'ont
pas de `rules.md`** : leurs garanties sont celles d'un ADR, vérifiées par leurs tests.

## Exemples canoniques

Aucun exemple inventé : on pointe le code qui fait foi.

| Ce qu'on cherche | Où le lire |
| --- | --- |
| Un test nommé par sa règle, avec ses trois cas | `internal/config/parse_test.go` |
| Une valeur qui ne s'imprime jamais | `internal/config/secret.go` — `String`, `GoString`, `LogValue`, `MarshalYAML`, `IsZero` |
| Une erreur qui nomme ce qui ne va pas, et où | `internal/config/resolve.go` |
| Une commande fine : lire, appeler, rendre | `internal/cli/config.go` |
| Un test sur une base réelle | `internal/state/open_test.go`, `internal/state/constraints_test.go` |
| Un schéma qui fait respecter un ADR | `internal/state/migrations/0001_initial.sql` |
| Un appel sortant derrière la porte | `internal/egress/egress.go` — `Gate.Dial` |
| Une règle d'architecture vérifiée, avec sa fixture violante | `internal/arch/` |

### Une commande cobra

Une commande **lit, appelle un cas d'usage, rend**. Elle ne décide rien.

- Elle écrit sur `cmd.OutOrStdout()`, jamais sur `os.Stdout` : sans cela elle n'est pas testable.
- Elle renvoie une erreur, elle n'appelle pas `os.Exit` : `cmd/koffr/main.go` est le seul endroit
  qui sort en code 1.
- `SilenceUsage` et `SilenceErrors` sont posés sur la racine : une erreur métier n'affiche pas
  l'aide.
- `internal/cli` **n'importe jamais `internal/state`** (`AR-04`).

### Une erreur

Un message d'erreur est **en anglais** (ADR-0003), et il dit trois choses : ce qui ne va pas, **où**
(fichier, ligne, identifiant de la base), et ce qu'on peut y faire quand c'est connu. `E-042` en
fait une exigence : nommer la version attendue, les versions trouvées et la commande de correction.

```go
return fmt.Errorf("%s: %s are given together; keep one", field.where, strings.Join(forms, " and "))
```

Envelopper avec `%w` quand l'appelant peut vouloir `errors.Is`. Le linter `wrapcheck` refuse une
erreur d'un autre paquet renvoyée nue.

### Un secret

Tout ce qui est sensible passe par `config.Secret` : il ne s'imprime ni par `%v`, ni par `%+v`, ni
par `%#v`, ni par `slog`, ni par YAML, et ne sort que par `Expose()` — un appel greppable, à
regarder en revue. `internal/obs` masque en plus toute clé d'attribut qui **nomme** un secret.

### Un test sur base réelle

`t.TempDir()`, `state.Open(dir)`, `t.Cleanup` pour fermer. Pas de simulacre : SQLite coûte quelques
millisecondes et ce qu'on veut vérifier — `CHECK`, `STRICT`, clés étrangères — n'existe que dans un
vrai moteur.

## Données

Fixé par ADR-0006 et rappelé dans `CLAUDE.md` § Données : horodatages RFC 3339 **UTC** contraints
par `GLOB`, statuts en toutes lettres contraints par `CHECK`, tailles en octets et durées en
millisecondes, **aucun flottant**, `created_at` / `updated_at` partout, tables `STRICT`.

Une évolution de schéma est une **nouvelle** migration numérotée dans
`internal/state/migrations/`, jamais une modification d'une migration déjà appliquée. Le SQL se
relit avant commit, et on le dit dans la tâche.

## Écritures externes

Deux régimes, ADR-0008 :

- **Fonctionnelles** — `internal/store` et `internal/engine` écrivent **réellement**, y compris en
  développement : c'est la fonction du produit. Les tests tournent contre des conteneurs éphémères,
  jamais contre un tiers réel.
- **D'exploitation** — SMTP, webhook, liaison, téléchargement d'outils : par `internal/egress`,
  dont le mode par défaut est le **puits**. Une configuration absente met en puits, **jamais en
  erreur**, jamais vers une valeur réelle.

Aucun hôte, jeton, clé ou destinataire dans le code ni en base. Tout vient de la configuration
validée au démarrage, avec les trois formes de `E-033`.

## Interface locale (à partir du lot 7)

Anglais (ADR-0003), pas de phrase d'explication : des champs, des valeurs, des libellés courts, un
message au moment de l'action. Chaque vue a son état vide, son état d'erreur et son état de
chargement. `internal/httpd` **écoute** ; il n'appelle pas — un appel sortant passe par `egress`.
La restauration n'y est pas déclenchable : ADR-0007 la réduit à l'affichage de la commande.

## Avant de dire « terminé »

**Après avoir modifié un fichier par script, relire le fichier**, pas seulement le code de retour :
un script d'édition qui échoue à mi-parcours n'écrit rien, et le commit part quand même.

`mise run verify` **vert, code de retour lu** ; la règle est dans `rules.md` avec sa source et le
nom de son test ; les frontières tiennent (`internal/arch` et le lint) ; la migration est relue ; le
code est en anglais et le pilotage en français ; la case du plan est cochée **au moment où la tâche
passe**, pas en bloc à la fin.
