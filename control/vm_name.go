package main

import (
  "context"
  "crypto/rand"
  "errors"
  "fmt"
  "math/big"
  "regexp"
  "strings"

  "github.com/jackc/pgx/v5/pgconn"
)

// A VM's name is what proxy routes http by, so it is a fleet-wide identifier
// rather than a label: unique, and shaped like the DNS label it becomes. A name
// the caller leaves empty is generated -- two words in the style of a docker
// container name -- because a VM with no name would have no route, and asking
// someone to invent a globally unique string is asking them to fail.
//
// crypto/rand rather than math/rand: the name ends up in a URL that reaches a
// guest, and a predictable sequence would let someone who has seen a few names
// guess the ones that come next.

// Bounds match the vms_name_shape constraint. Duplicated rather than derived
// because the database rejects a bad name and the API explains one, and those
// are different jobs -- but they must agree, so they are stated next to the
// error text a caller sees.
const (
  vmNameMinLen = 3
  vmNameMaxLen = 52
)

// vmNamePattern is lowercase alphanumeric words joined by single hyphens:
// "hellokitty" and "hello-temporal-kitty" pass, "hello--kitty" does not.
var vmNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// vmNameAttempts bounds the retry loop on a name that is already taken. With the
// list sizes below there are over twenty thousand combinations, so a handful of
// attempts covers a fleet far larger than one of these is likely to hold; the
// bound exists so a genuinely exhausted list fails instead of spinning.
const vmNameAttempts = 6

// vmNameConstraint is the unique constraint on vms.name, matched by name so a
// taken name is distinguished from every other unique violation -- notably the
// (agent_id, vm_id) index, which a retry under a different name would never
// resolve.
const vmNameConstraint = "vms_name_key"

// validateVMName checks a caller-supplied name. Empty is not an error: it is the
// request to generate one, and the caller decides what that means.
func validateVMName(s string) (string, error) {
  name := strings.ToLower(strings.TrimSpace(s))
  if name == "" {
    return "", nil
  }
  if len(name) < vmNameMinLen || len(name) > vmNameMaxLen {
    return "", fmt.Errorf("a name must be between %d and %d characters", vmNameMinLen, vmNameMaxLen)
  }
  if !vmNamePattern.MatchString(name) {
    return "", errors.New("a name must be lowercase letters, digits and single hyphens, like \"hello-kitty\"")
  }
  return name, nil
}

// randomVMName returns a name like "interesting-hawking".
func randomVMName() (string, error) {
  adj, err := pickWord(vmNameAdjectives)
  if err != nil {
    return "", err
  }
  noun, err := pickWord(vmNameNouns)
  if err != nil {
    return "", err
  }
  return adj + "-" + noun, nil
}

func pickWord(list []string) (string, error) {
  n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
  if err != nil {
    return "", fmt.Errorf("could not generate a vm name: %w", err)
  }
  return list[n.Int64()], nil
}

// withVMName runs insert under a usable name for a VM someone asked for. An
// empty preferred means "generate one", and each collision is redrawn; a
// preferred name that is taken comes back as errVMNameTaken, because a caller
// who chose a name has to be told they did not get it rather than quietly
// handed a different one.
//
// The insert is handed the name rather than choosing its own so the retry lives
// in one place: both create paths and the adopt path need it, and one that
// forgot would fail for a reason unrelated to the request.
func withVMName(ctx context.Context, preferred string, insert func(ctx context.Context, name string) error) error {
  return nameLoop(ctx, preferred, true, insert)
}

// withAdoptedVMName is the same for a VM this server did not ask for: the
// preferred name is the host's, and if it is taken there is nobody to report
// that to, so a generated one is used instead. Refusing would mean a VM that is
// really running never appears at all.
func withAdoptedVMName(ctx context.Context, preferred string, insert func(ctx context.Context, name string) error) error {
  return nameLoop(ctx, preferred, false, insert)
}

func nameLoop(ctx context.Context, preferred string, reportTaken bool, insert func(ctx context.Context, name string) error) error {
  for attempt := range vmNameAttempts {
    name := preferred
    if attempt > 0 || name == "" {
      generated, err := randomVMName()
      if err != nil {
        return err
      }
      name = generated
    }
    err := insert(ctx, name)
    if err == nil || !isVMNameTaken(err) {
      return err
    }
    if attempt == 0 && preferred != "" && reportTaken {
      return errVMNameTaken
    }
  }
  return errors.New("could not find an unused vm name")
}

// errVMNameTaken is the one failure a caller reports rather than retries.
var errVMNameTaken = errors.New("that name is already taken")

func isVMNameTaken(err error) bool {
  var pgErr *pgconn.PgError
  return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == vmNameConstraint
}

// The lists are ASCII-only and hyphen-free on purpose: the two halves are joined
// with a hyphen, and a word carrying one of its own would produce the doubled
// hyphen the name constraint rejects.

var vmNameAdjectives = []string{
  "admiring", "adoring", "affectionate", "agitated", "amazing", "angry", "awesome",
  "beautiful", "blissful", "bold", "boring", "brave", "busy", "charming", "clever",
  "compassionate", "competent", "condescending", "confident", "cool", "cranky",
  "crazy", "curious", "dazzling", "determined", "distracted", "dreamy", "eager",
  "ecstatic", "elastic", "elated", "elegant", "eloquent", "epic", "exciting",
  "fervent", "festive", "flamboyant", "focused", "friendly", "frosty", "funny",
  "gallant", "gifted", "goofy", "gracious", "great", "happy", "hardcore",
  "heuristic", "hopeful", "hungry", "infallible", "inspiring", "intelligent",
  "interesting", "jolly", "jovial", "keen", "kind", "laughing", "loving", "lucid",
  "magical", "modest", "musing", "mystifying", "naughty", "nervous", "nice",
  "nifty", "nostalgic", "objective", "optimistic", "peaceful", "pedantic",
  "pensive", "practical", "priceless", "quirky", "quizzical", "recursing",
  "relaxed", "reverent", "romantic", "serene", "sharp", "silly", "sleepy",
  "stoic", "strange", "stupefied", "suspicious", "sweet", "tender", "thirsty",
  "trusting", "unruffled", "upbeat", "vibrant", "vigilant", "vigorous", "wizardly",
  "wonderful", "xenodochial", "youthful", "zealous",
}

var vmNameNouns = []string{
  "agnesi", "albattani", "allen", "almeida", "altman", "ampere", "archimedes",
  "ardinghelli", "aryabhata", "austin", "babbage", "banach", "bardeen", "bartik",
  "bassi", "bell", "benz", "bhabha", "bhaskara", "blackwell", "bohr", "booth",
  "borg", "bose", "bouman", "boyd", "brahmagupta", "brattain", "brown", "buck",
  "burnell", "cannon", "carson", "cartwright", "carver", "cerf", "chandrasekhar",
  "chaplygin", "chatelet", "chatterjee", "chebyshev", "clarke", "cohen", "colden",
  "cori", "cray", "curie", "curran", "darwin", "davinci", "dewdney", "dhawan",
  "dijkstra", "dirac", "easley", "edison", "einstein", "elbakyan", "elgamal",
  "elion", "ellis", "engelbart", "euclid", "euler", "faraday", "feistel", "fermat",
  "fermi", "feynman", "franklin", "gagarin", "galileo", "galois", "ganguly",
  "gates", "gauss", "germain", "goldberg", "goldstine", "goodall", "gould",
  "hamilton", "hawking", "heisenberg", "hermann", "herschel", "hertz", "hodgkin",
  "hofstadter", "hoover", "hopper", "hugle", "hypatia", "jackson", "jang",
  "jemison", "jennings", "jepsen", "johnson", "joliot", "jones", "kalam", "kapitsa",
  "kare", "keldysh", "kepler", "khayyam", "khorana", "kilby", "kirch", "knuth",
  "kowalevski", "lalande", "lamarr", "lamport", "leakey", "leavitt", "lehmann",
  "lichterman", "liskov", "lovelace", "lumiere", "mahavira", "margulis", "matsumoto",
  "maxwell", "mayer", "mccarthy", "mcclintock", "mclaren", "mclean", "mcnulty",
  "meitner", "mendel", "mendeleev", "meninsky", "merkle", "mestorf", "mirzakhani",
  "montalcini", "moore", "morse", "murdock", "napier", "nash", "neumann", "newton",
  "nightingale", "nobel", "noether", "northcutt", "noyce", "panini", "pare",
  "pascal", "pasteur", "payne", "perlman", "pike", "planck", "poincare", "poitras",
  "proskuriakova", "ptolemy", "raman", "ramanujan", "rhodes", "ride", "ritchie",
  "robinson", "roentgen", "rosalind", "rubin", "saha", "sammet", "sanderson",
  "satoshi", "shamir", "shannon", "shaw", "shirley", "shockley", "shtern",
  "sinoussi", "smola", "snyder", "solomon", "spence", "stonebraker", "sutherland",
  "swanson", "swartz", "swirles", "taussig", "tesla", "tharp", "thompson",
  "torvalds", "tereshkova", "turing", "varahamihira", "vaughan", "villani",
  "visvesvaraya", "volhard", "wescoff", "wilbur", "wiles", "williams", "williamson",
  "wilson", "wing", "wozniak", "wright", "yalow", "yonath", "zhukovsky",
}
