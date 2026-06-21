package config

import "time"

// Overrides holds optional command-line overrides. A nil pointer field means
// "not set on the command line" and leaves the config value untouched.
type Overrides struct {
	Name          *string
	Repo          *string
	Task          *string
	MaxWall       *time.Duration
	MaxIters      *int
	MaxTurns      *int
	Model         *string
	Claude        *string
	GitHub        *string
	PerRunWall    *time.Duration
	MaxNoProgress *int
	Cooldown      *time.Duration
	MaxRuns       *int
}

// Apply layers the set overrides over c and re-validates the result. It returns
// the merged config; the receiver is not mutated.
func (c Config) Apply(o Overrides) (Config, error) {
	if o.Name != nil {
		c.Name = *o.Name
	}
	if o.Repo != nil {
		c.Repo = *o.Repo
	}
	if o.Task != nil {
		c.Task = *o.Task
	}
	if o.MaxWall != nil {
		c.Guards.MaxWall = Duration(*o.MaxWall)
	}
	if o.MaxIters != nil {
		c.Guards.MaxIters = *o.MaxIters
	}
	if o.MaxTurns != nil {
		c.Guards.MaxTurns = *o.MaxTurns
	}
	if o.Model != nil {
		c.Model.Name = *o.Model
	}
	if o.Claude != nil {
		c.Auth.Claude = *o.Claude
	}
	if o.GitHub != nil {
		c.Auth.GitHub = *o.GitHub
	}
	if o.PerRunWall != nil {
		c.Autorun.PerRunWall = Duration(*o.PerRunWall)
	}
	if o.MaxNoProgress != nil {
		c.Autorun.MaxNoProgress = *o.MaxNoProgress
	}
	if o.Cooldown != nil {
		c.Autorun.Cooldown = Duration(*o.Cooldown)
	}
	if o.MaxRuns != nil {
		c.Autorun.MaxRuns = *o.MaxRuns
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
