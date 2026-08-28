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

	"control/internal/db"
)

const (
	vmNameMinLen = 3
	vmNameMaxLen = 52
)

var vmNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const vmNameAttempts = 6

const vmNameConstraint = "vms_name_key"

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

var systemReservedVMNames = []string{proxySiteLabel, proxyConsoleLabel, "console", "int"}

func vmNameAllowed(ctx context.Context, q *db.Queries, name string) error {
	if name == "" {
		return nil
	}
	for _, list := range [][]string{systemReservedVMNames, parseReservedVMNames(setting(ctx, q, settingReservedVMNames))} {
		for _, r := range list {
			if r == name {
				return fmt.Errorf("%q is reserved and cannot be used as a name", name)
			}
		}
	}
	return nil
}

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

func withVMName(ctx context.Context, preferred string, insert func(ctx context.Context, name string) error) error {
	return nameLoop(ctx, preferred, true, insert)
}

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

var errVMNameTaken = errors.New("that name is already taken")

func isVMNameTaken(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == vmNameConstraint
}

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
