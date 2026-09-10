package rssnip

import (
	"context"
	"embed"
	"io"

	"github.com/Songmu/skillsmith"
)

//go:embed skills
var skillsFS embed.FS

func runSkills(ctx context.Context, args []string, outStream, errStream io.Writer) error {
	smith, err := newSkillSmith(outStream, errStream)
	if err != nil {
		return err
	}
	return smith.Run(ctx, args)
}

func newSkillSmith(outStream, errStream io.Writer) (*skillsmith.Smith, error) {
	smith, err := skillsmith.New(cmdName, version, skillsFS)
	if err != nil {
		return nil, err
	}
	smith.OutWriter = outStream
	smith.ErrWriter = errStream
	return smith, nil
}
